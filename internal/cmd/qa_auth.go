package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

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
	// Check Docker is available.
	if err := dockerAvailable(); err != nil {
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
	if !imageExists(baseTag) {
		return fmt.Errorf("base image %q not found; run 'h2 qa setup' first", baseTag)
	}

	// Check if authed image already exists.
	if imageExists(authTag) && !force {
		fmt.Fprintf(os.Stderr, "Authed image %q already exists. Use --force to overwrite.\n", authTag)
		return fmt.Errorf("authed image already exists (use --force to overwrite)")
	}

	// Start interactive container.
	containerName := "h2-qa-auth-session"

	// Remove any leftover container from a previous failed auth.
	exec.Command("docker", "rm", "-f", containerName).Run()

	fmt.Fprintf(os.Stderr, "Starting interactive container from %s...\n", baseTag)
	fmt.Fprintf(os.Stderr, "Log into Claude Code by running: claude\n")
	fmt.Fprintf(os.Stderr, "When done, type 'exit' to save auth state.\n\n")

	// docker run -it --name <name> <base-tag> /bin/sh
	runCmd := exec.Command("docker", "run", "-it", "--name", containerName, baseTag, "/bin/sh")
	runCmd.Stdin = os.Stdin
	runCmd.Stdout = os.Stdout
	runCmd.Stderr = os.Stderr

	runErr := runCmd.Run()

	// Commit the container regardless of exit code (user may have ctrl+d'd).
	fmt.Fprintf(os.Stderr, "\nCommitting auth state to %s...\n", authTag)
	_, stderr, commitErr := dockerExec("commit", containerName, authTag)
	if commitErr != nil {
		// Clean up container before returning error.
		exec.Command("docker", "rm", "-f", containerName).Run()
		return fmt.Errorf("docker commit failed: %s", stderr)
	}

	// Remove the stopped container.
	exec.Command("docker", "rm", "-f", containerName).Run()

	if runErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: container exited with error: %v\n", runErr)
		fmt.Fprintf(os.Stderr, "Auth image was still committed. You may want to verify auth by running 'h2 qa auth' again.\n")
	}

	fmt.Fprintf(os.Stderr, "Auth image saved: %s\n", authTag)
	return nil
}
