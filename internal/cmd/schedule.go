package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"h2/internal/session/message"
)

func newScheduleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schedule",
		Short: "Manage schedules on an agent",
		Long:  "Register, list, and remove RRULE-based schedules on a running agent.",
	}

	cmd.AddCommand(
		newScheduleAddCmd(),
		newScheduleListCmd(),
		newScheduleRemoveCmd(),
	)
	return cmd
}

func newScheduleAddCmd() *cobra.Command {
	var (
		rrule         string
		start         string
		condition     string
		conditionMode string
		execCmd       string
		msg           string
		from          string
		priority      string
		name          string
	)

	cmd := &cobra.Command{
		Use:   "add <agent-name>",
		Short: "Register a schedule on an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName := args[0]

			if execCmd == "" && msg == "" {
				return fmt.Errorf("either --exec or --message is required")
			}
			if execCmd != "" && msg != "" {
				return fmt.Errorf("--exec and --message are mutually exclusive")
			}

			id := uuid.New().String()[:8]

			spec := &message.ScheduleSpec{
				ID:            id,
				Name:          name,
				Start:         start,
				RRule:         rrule,
				Condition:     condition,
				ConditionMode: conditionMode,
				Exec:          execCmd,
				Message:       msg,
				From:          from,
				Priority:      priority,
			}

			resp, err := sendSocketRequest(agentName, &message.Request{
				Type:     "schedule_add",
				Schedule: spec,
			})
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("schedule add failed: %s", resp.Error)
			}

			fmt.Fprintf(os.Stderr, "Note: dynamically registered, will not survive agent restart.\n")
			fmt.Println(resp.ScheduleID)
			return nil
		},
	}

	cmd.Flags().StringVar(&rrule, "rrule", "", "RRULE string (RFC 5545)")
	cmd.Flags().StringVar(&start, "start", "", "Start time (RFC 3339); defaults to now")
	cmd.Flags().StringVar(&condition, "condition", "", "Shell command condition (exit 0 = pass)")
	cmd.Flags().StringVar(&conditionMode, "condition-mode", "", "Condition mode: run_if, stop_when, run_once_when")
	cmd.Flags().StringVar(&execCmd, "exec", "", "Shell command action")
	cmd.Flags().StringVar(&msg, "message", "", "Message action (injected into agent PTY)")
	cmd.Flags().StringVar(&from, "from", "", "Sender identity for message action (default: h2-schedule)")
	cmd.Flags().StringVar(&priority, "priority", "", "Message priority (interrupt|normal|idle-first|idle)")
	cmd.Flags().StringVar(&name, "name", "", "Human-readable schedule name")
	_ = cmd.MarkFlagRequired("rrule")

	return cmd
}

func newScheduleListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <agent-name>",
		Short: "List schedules on an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := sendSocketRequest(args[0], &message.Request{
				Type: "schedule_list",
			})
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("schedule list failed: %s", resp.Error)
			}

			if len(resp.Schedules) == 0 {
				fmt.Println("No schedules registered.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tRRULE\tMODE\tACTION")
			for _, s := range resp.Schedules {
				action := "exec"
				if s.Message != "" {
					action = "message"
				}
				mode := s.ConditionMode
				if mode == "" {
					mode = "run_if"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					s.ID, s.Name, s.RRule, mode, action)
			}
			w.Flush()
			return nil
		},
	}
}

func newScheduleRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <agent-name> <schedule-id>",
		Short: "Remove a schedule from an agent",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := sendSocketRequest(args[0], &message.Request{
				Type:       "schedule_remove",
				ScheduleID: args[1],
			})
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("schedule remove failed: %s", resp.Error)
			}
			fmt.Println("Removed.")
			return nil
		},
	}
}
