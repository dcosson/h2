package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newQASetupCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Build the QA Docker image",
		Long:  "Builds a Docker image from the project's Dockerfile for use in QA runs.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runQASetup(configPath)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "Path to h2-qa.yaml config file")

	return cmd
}

func runQASetup(configPath string) error {
	// Check Docker is available.
	if err := dockerAvailable(); err != nil {
		return err
	}

	// Load config.
	cfg, err := DiscoverQAConfig(configPath)
	if err != nil {
		return err
	}

	// Resolve Dockerfile path.
	dockerfile := cfg.ResolvedDockerfile()
	if dockerfile == "" {
		return fmt.Errorf("sandbox.dockerfile is required in h2-qa.yaml")
	}
	if _, err := os.Stat(dockerfile); os.IsNotExist(err) {
		return fmt.Errorf("Dockerfile not found: %s", dockerfile)
	}

	// Determine the config file path for tag generation.
	// Use the explicit path or re-discover.
	tagPath := configPath
	if tagPath == "" {
		// Resolve to an absolute path for deterministic tagging.
		candidates := []string{"h2-qa.yaml", "qa/h2-qa.yaml"}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				tagPath = c
				break
			}
		}
	}
	tag := projectImageTag(tagPath)

	// Build docker command args.
	buildArgs := []string{"build", "-f", dockerfile, "-t", tag}

	for k, v := range cfg.Sandbox.BuildArgs {
		buildArgs = append(buildArgs, "--build-arg", fmt.Sprintf("%s=%s", k, v))
	}

	// Use the Dockerfile's directory as the build context.
	buildArgs = append(buildArgs, cfg.configDir)

	fmt.Fprintf(os.Stderr, "Building QA image %s from %s...\n", tag, dockerfile)

	_, stderr, err := dockerExec(buildArgs...)
	if err != nil {
		return fmt.Errorf("docker build failed: %s", stderr)
	}

	// Get image size for display.
	size, _, _ := dockerExec("image", "inspect", "--format", "{{.Size}}", tag)

	fmt.Fprintf(os.Stderr, "QA image built: %s", tag)
	if size != "" {
		fmt.Fprintf(os.Stderr, " (%s bytes)", size)
	}
	fmt.Fprintln(os.Stderr)

	return nil
}
