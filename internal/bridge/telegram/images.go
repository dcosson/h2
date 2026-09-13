package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxImageBytes = 10 * 1024 * 1024
const imageTimeout = 60 * time.Second

// imageFormat bounds decoding work and rejects files that are not JPEG or PNG.
func imageFormat(data []byte) (string, error) {
	if len(data) == 0 || len(data) > maxImageBytes {
		return "", fmt.Errorf("image must be between 1 byte and 10 MiB")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") {
		return "", fmt.Errorf("image must be JPEG or PNG")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width) > 40_000_000/int64(cfg.Height) {
		return "", fmt.Errorf("image dimensions exceed 40 megapixels")
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return "", fmt.Errorf("invalid image data")
	}
	return format, nil
}

// imageRequest never includes token-bearing URLs or remote response bodies in
// errors, and does not follow redirects to a different download endpoint.
func (t *Telegram) imageRequest(req *http.Request) (*http.Response, error) {
	client := t.client
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return nil, fmt.Errorf("telegram image request: %w", req.Context().Err())
		}
		return nil, fmt.Errorf("telegram image request failed")
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("telegram image request: HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

// SendImage uploads an explicitly selected local image. Caption text is plain
// text, like Send; a long caption is rejected rather than silently truncated.
func (t *Telegram) SendImage(ctx context.Context, filename, caption string) error {
	if utf8.RuneCountInString(caption) > 1024 {
		return fmt.Errorf("image caption exceeds 1024 characters")
	}
	info, err := os.Lstat(filename)
	if err != nil {
		return fmt.Errorf("read image: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxImageBytes {
		return fmt.Errorf("image must be a regular file of at most 10 MiB")
	}
	f, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("open image: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxImageBytes+1))
	if err != nil {
		return fmt.Errorf("read image: %w", err)
	}
	format, err := imageFormat(data)
	if err != nil {
		return err
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("chat_id", strconv.FormatInt(t.ChatID, 10)); err != nil {
		return err
	}
	if err := mw.WriteField("caption", caption); err != nil {
		return err
	}
	part, err := mw.CreateFormFile("photo", "image."+format)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, imageTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.apiURL("sendPhoto"), &body)
	if err != nil {
		return fmt.Errorf("invalid Telegram endpoint")
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := t.imageRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var result apiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return fmt.Errorf("telegram sendPhoto: invalid response")
	}
	if !result.OK {
		return fmt.Errorf("telegram sendPhoto: API rejected image")
	}
	return nil
}

type photoSize struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size,omitempty"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type document struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

func (m *message) imageFile() (string, int64) {
	var best photoSize
	for _, p := range m.Photo {
		if best.FileID == "" || int64(p.Width)*int64(p.Height) > int64(best.Width)*int64(best.Height) {
			best = p
		}
	}
	if best.FileID != "" {
		return best.FileID, best.FileSize
	}
	if m.Document != nil && (m.Document.MimeType == "image/jpeg" || m.Document.MimeType == "image/png") {
		return m.Document.FileID, m.Document.FileSize
	}
	return "", 0
}

func (t *Telegram) receiveImage(ctx context.Context, fileID string, size int64) (string, error) {
	if t.AttachmentDir == "" {
		return "", fmt.Errorf("image storage is not configured")
	}
	if size > maxImageBytes {
		return "", fmt.Errorf("image exceeds 10 MiB")
	}
	ctx, cancel := context.WithTimeout(ctx, imageTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.apiURL("getFile"), strings.NewReader(url.Values{"file_id": {fileID}}.Encode()))
	if err != nil {
		return "", fmt.Errorf("invalid Telegram endpoint")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.imageRequest(req)
	if err != nil {
		return "", err
	}
	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
			FileSize int64  `json:"file_size"`
		} `json:"result"`
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result)
	resp.Body.Close()
	if err != nil || !result.OK {
		return "", fmt.Errorf("telegram getFile: invalid response")
	}
	fp := result.Result.FilePath
	if fp == "" || strings.HasPrefix(fp, "/") || path.Clean(fp) != fp || strings.HasPrefix(fp, "..") || strings.ContainsAny(fp, "\\?#%:\r\n") {
		return "", fmt.Errorf("telegram getFile: invalid file path")
	}
	if result.Result.FileSize > maxImageBytes {
		return "", fmt.Errorf("image exceeds 10 MiB")
	}
	base := t.BaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, base+"/file/bot"+t.Token+"/"+fp, nil)
	if err != nil {
		return "", fmt.Errorf("invalid Telegram file endpoint")
	}
	resp, err = t.imageRequest(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return "", fmt.Errorf("telegram image download failed")
	}
	format, err := imageFormat(data)
	if err != nil {
		return "", err
	}
	dir, err := filepath.Abs(t.AttachmentDir)
	if err != nil {
		return "", fmt.Errorf("resolve image storage: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create image storage: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("image storage must be a private directory")
	}
	f, err := os.CreateTemp(dir, "image-*."+format)
	if err != nil {
		return "", fmt.Errorf("store image: %w", err)
	}
	filename := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(filename)
		return "", fmt.Errorf("store image: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(filename)
		return "", fmt.Errorf("store image: %w", err)
	}
	return filename, nil
}
