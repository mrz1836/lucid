package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// newSecretCmd wires the names-only reference catalog. Its children map
// one-to-one onto the three catalog intents.
func newSecretCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "secret",
		Short: "Maintain a catalog of secret reference names",
		Long: `secret maintains a names-only reference catalog. Secret values remain
outside Lucid; this command records only a handle, an optional note, and its
creation time. Catalog history is append-only, so remove records a tombstone.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	parent.AddCommand(newSecretAddCmd())
	parent.AddCommand(newSecretListCmd())
	parent.AddCommand(newSecretRemoveCmd())
	return parent
}

// newSecretAddCmd registers one reference name and optional note.
func newSecretAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Register a reference name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := secretRouter(cmd)
			if err != nil {
				return err
			}
			note, _ := cmd.Flags().GetString(flagNote)
			res, err := r.SecretAdd(router.SecretAddRequest{
				Name:   args[0],
				Note:   note,
				Now:    clockNow(),
				Source: sourceCLI,
			})
			if err != nil {
				return emitErr(cmd, err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
	cmd.Flags().String(flagNote, "", "Optional note kept verbatim (maximum 256 characters)")
	return cmd
}

// newSecretListCmd prints the live catalog in human-first prose or as the
// stable folded reference shape under --json.
func newSecretListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered reference names",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := secretRouter(cmd)
			if err != nil {
				return err
			}
			refs, err := r.SecretList()
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), refs)
			}
			if len(refs) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No secret references registered.")
				return nil
			}
			for _, ref := range refs {
				if ref.Note == "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s (created %s)\n", ref.Name, ref.CreatedAt)
					continue
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s — %s (created %s)\n", ref.Name, ref.Note, ref.CreatedAt)
			}
			return nil
		},
	}
}

// newSecretRemoveCmd appends a tombstone for one live reference name.
func newSecretRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a reference name from the live catalog",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := secretRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.SecretRemove(args[0], clockNow(), sourceCLI)
			if err != nil {
				return emitErr(cmd, err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
}

// secretRouter performs the shared stateful-command boot and lazily prepares
// the catalog directory, including for an empty list.
func secretRouter(cmd *cobra.Command) (*router.Router, error) {
	r, err := bootedRouter(cmd)
	if err != nil {
		return nil, err
	}
	if err = r.Store().ScaffoldSecrets(); err != nil {
		return nil, fmt.Errorf("lucid secret: %w", err)
	}
	return r, nil
}
