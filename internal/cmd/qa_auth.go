package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

// qaAuthDeps holds injectable dependencies for the auth flow.
// Production code uses the defaults; tests can override.
type qaAuthDeps struct {
	dockerAvailable func() error
	imageExists     func(tag string) bool
	dockerExec      func(args ...string) (string, string, error)
	runInteractive  func(name, image string) error
	removeContainer func(name string)
}

var defaultQAAuthDeps = qaAuthDeps{
	dockerAvailable: dockerAvailable,
	imageExists:     imageExists,
	dockerExec:      dockerExec,
	runInteractive: func(name, image string) error {
		cmd := exec.Command("docker", "run", "-it", "--name", name, image, "/bin/sh")
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	},
	removeContainer: func(name string) {
		exec.Command("docker", "rm", "-f", name).Run()
	},
}

// authContainerName returns a project-specific container name for auth sessions.
func authContainerName(configPath string) string {
	return fmt.Sprintf("h2-qa-auth-%s", projectHash(configPath))
}

func newQAAuthCmd() *cobra.Command {
	var configPath string
	var force bool

	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate Claude Code in the QA container",
		Long:  "Starts an interactive container to authenticate Claude Code. The auth state is committed as a new image layer.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runQAAuth(configPath, force)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "Path to h2-qa.yaml config file")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing authed image without confirmation")

	return cmd
}

func runQAAuth(configPath string, force bool) error {
	return runQAAuthWithDeps(configPath, force, defaultQAAuthDeps)
}

func runQAAuthWithDeps(configPath string, force bool, deps qaAuthDeps) error {
	// Check Docker is available.
	if err := deps.dockerAvailable(); err != nil {
		return err
	}

	// Load config to determine tags.
	cfg, err := DiscoverQAConfig(configPath)
	if err != nil {
		return err
	}

	baseTag := projectImageTag(cfg.configPath)
	authTag := authedImageTag(cfg.configPath)

	// Verify base image exists.
	if !deps.imageExists(baseTag) {
		return fmt.Errorf("base image %q not found; run 'h2 qa setup' first", baseTag)
	}

	// Check if authed image already exists.
	if deps.imageExists(authTag) && !force {
		fmt.Fprintf(os.Stderr, "Authed image %q already exists. Use --force to overwrite.\n", authTag)
		return fmt.Errorf("authed image already exists (use --force to overwrite)")
	}

	containerName := authContainerName(cfg.configPath)

	// Remove any leftover container from a previous failed auth.
	deps.removeContainer(containerName)

	fmt.Fprintf(os.Stderr, "Starting interactive container from %s...\n", baseTag)
	fmt.Fprintf(os.Stderr, "Log into Claude Code by running: claude\n")
	fmt.Fprintf(os.Stderr, "When done, type 'exit' to save auth state.\n\n")

	runErr := deps.runInteractive(containerName, baseTag)

	// Commit the container regardless of exit code (user may have ctrl+d'd).
	fmt.Fprintf(os.Stderr, "\nCommitting auth state to %s...\n", authTag)
	_, stderr, commitErr := deps.dockerExec("commit", containerName, authTag)
	if commitErr != nil {
		// Clean up container before returning error.
		deps.removeContainer(containerName)
		return fmt.Errorf("docker commit failed: %s", stderr)
	}

	// Remove the stopped container.
	deps.removeContainer(containerName)

	if runErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: container exited with error: %v\n", runErr)
		fmt.Fprintf(os.Stderr, "Auth image was still committed. You may want to verify auth by running 'h2 qa auth' again.\n")
	}

	fmt.Fprintf(os.Stderr, "Auth image saved: %s\n", authTag)
	return nil
}
