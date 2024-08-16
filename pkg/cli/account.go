package storagecli

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

func (a AppProvider) listAccounts(c *cli.Context) error {
	app, err := a(c)
	if err != nil {
		return err
	}

	accts, err := app.Accounts.ListAll(c.Context)
	if err != nil {
		return err
	}

	fmt.Printf("ID\tNAME\tCREATED\n")
	for _, acct := range accts {
		fmt.Printf("%v\t%v\t%v\n", acct.ID_, acct.Name_, acct.CreatedAt_)
	}
	return nil
}

func (a AppProvider) createAccount(c *cli.Context) error {
	app, err := a(c)
	if err != nil {
		return err
	}

	if c.NArg() != 1 {
		return cli.Exit("Account name is required", 2)
	}

	acct, err := app.Accounts.Create(c.Context, c.Args().First())
	if err != nil {
		return err
	}

	fmt.Println(acct.ID_)

	return nil
}

func (a AppProvider) deleteAccount(c *cli.Context) error {
	app, err := a(c)
	if err != nil {
		return err
	}

	if c.NArg() != 1 {
		return cli.Exit("Account name is required", 2)
	}

	id, err := app.Accounts.DeleteByName(c.Context, c.Args().First())
	if err != nil {
		return err
	}

	fmt.Println(id)

	return nil
}
