package storagecli

import (
	"github.com/foxcpp/maddy-storage/internal/usecase"
	"github.com/urfave/cli/v2"
)

type AppProvider func(ctx *cli.Context) (App, error)

type App struct {
	Accounts usecase.Account
	Folders  usecase.Folder
	Message  usecase.Message
}

func BuildCommands(provider AppProvider) cli.Commands {
	return cli.Commands{
		{
			Name:  "account",
			Usage: "Storage account management",
			Subcommands: []*cli.Command{
				{
					Name:   "list",
					Usage:  "List existing accounts",
					Action: provider.listAccounts,
				},
				{
					Name:      "create",
					Usage:     "Create new account",
					Args:      true,
					ArgsUsage: "<account name>",
					Action:    provider.createAccount,
				},
				{
					Name:      "delete",
					Usage:     "Delete an account",
					Args:      true,
					ArgsUsage: "<account name>",
					Action:    provider.deleteAccount,
				},
			},
		},
		{
			Name:        "folder",
			Usage:       "Folders management",
			Subcommands: []*cli.Command{},
		},
		{
			Name:        "messages",
			Usage:       "Messages management",
			Subcommands: []*cli.Command{},
		},
	}
}
