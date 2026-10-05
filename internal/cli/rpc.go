package cli

import (
	"github.com/spf13/cobra"

	"github.com/federbenjamin/herdr-desk/internal/api"
)

// rpcCmd is what a client's [client] command runs on the home.
func (a *app) rpcCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rpc",
		Short: "Answer one JSON request from stdin with one JSON response on stdout (a client's [client] command runs it)",
		Args:  cobra.NoArgs,
		RunE: a.do(func(_ *cobra.Command, _ []string) error {
			c, err := a.config()
			if err != nil {
				return err
			}
			if c.IsClient() {
				return usage("this machine is a client of %s; rpc runs on the home", c.Client.Home)
			}
			return api.ServeRPC(a.ctx, a.paths, c, a.env.Stdin, a.env.Stdout)
		}),
	}
}
