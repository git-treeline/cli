package cmd

import (
	"encoding/json"
	"errors"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func init() {
	releaseCmd.AddCommand(newReleaseStatusCommand(""))
}

type releaseStatus struct {
	Schema      int                        `json:"schema"`
	OperationID string                     `json:"operation_id"`
	Outcome     string                     `json:"outcome"`
	Operation   *registry.ReleaseOperation `json:"operation"`
}

// Status only reads retained evidence. In particular, it does not reconcile a
// started phase, enroll allocations, load project config or run root update hooks.
func newReleaseStatusCommand(registryPath string) *cobra.Command {
	return &cobra.Command{
		Use:               "status <operation-id>",
		Short:             "Read a retained release operation as JSON without running cleanup",
		Args:              cobra.ExactArgs(1),
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		PersistentPostRun: func(*cobra.Command, []string) {},
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			response := releaseStatus{Schema: 1, OperationID: id}
			parsed, err := uuid.Parse(id)
			if err != nil || len(id) != 36 || parsed.String() != id {
				response.OperationID = ""
				response.Outcome = "invalid_operation_id"
			} else {
				response.Operation, err = registry.New(registryPath).GetRelease(id)
				switch {
				case err != nil:
					response.Outcome = "registry_unavailable"
				case response.Operation == nil:
					response.Outcome = "not_found"
				default:
					response.Outcome = response.Operation.Outcome()
				}
			}
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(response); err != nil {
				return err
			}
			if response.Operation == nil {
				return errors.New(response.Outcome)
			}
			return nil
		},
	}
}
