package storagecli

import (
	"fmt"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/usecase"
	"github.com/urfave/cli/v2"
)

func (a AppProvider) listFolders(c *cli.Context) error {
	app, err := a(c)
	if err != nil {
		return err
	}

	if c.NArg() != 1 {
		return cli.Exit("Account name is required", 2)
	}
	accountName := c.Args().First()

	acct, err := app.Accounts.GetByName(c.Context, accountName)
	if err != nil {
		return err
	}

	data, err := app.Folders.List(c.Context, acct.ID_, &usecase.ListOpts{
		SortAsTree: true,
	}, folder.OrderBySortOrder)
	if err != nil {
		return err
	}

	fmt.Printf("ID\tPATH\tROLE\n")
	for _, f := range data {
		fmt.Printf("%v\t%v\t%v\n", f.Folder.ID_, f.Folder.Path_, f.Folder.Role_)
	}
	return nil
}

func (a AppProvider) createFolder(c *cli.Context) error {
	app, err := a(c)
	if err != nil {
		return err
	}

	if c.NArg() < 1 {
		return cli.Exit("Account name is required", 2)
	}
	if c.NArg() < 2 {
		return cli.Exit("Folder name is required", 2)
	}
	accountName := c.Args().First()
	folderName := c.Args().Get(1)
	role := folder.RoleNone
	if c.NArg() == 3 {
		role = folder.Role(c.Args().Get(2))
	}

	acct, err := app.Accounts.GetByName(c.Context, accountName)
	if err != nil {
		return err
	}

	created, err := app.Folders.Create(c.Context, acct.ID_, folderName, role)
	if err != nil {
		return err
	}
	fmt.Println(created.ID_)
	return nil
}

func (a AppProvider) renameFolder(c *cli.Context) error {
	app, err := a(c)
	if err != nil {
		return err
	}

	if c.NArg() < 1 {
		return cli.Exit("Account name is required", 2)
	}
	if c.NArg() < 2 {
		return cli.Exit("Old folder path  is required", 2)
	}
	if c.NArg() < 3 {
		return cli.Exit("New folder path is required", 2)
	}
	accountName := c.Args().First()
	oldPath := c.Args().Get(1)
	newPath := c.Args().Get(2)

	acct, err := app.Accounts.GetByName(c.Context, accountName)
	if err != nil {
		return err
	}

	renamed, err := app.Folders.Rename(c.Context, acct.ID_, oldPath, newPath)
	if err != nil {
		return err
	}

	for _, f := range renamed {
		fmt.Println(f.ID, f.OldPath, f.NewPath)
	}
	return nil
}

func (a AppProvider) deleteFolder(c *cli.Context) error {
	app, err := a(c)
	if err != nil {
		return err
	}

	if c.NArg() < 1 {
		return cli.Exit("Account name is required", 2)
	}
	if c.NArg() < 2 {
		return cli.Exit("Folder name is required", 2)
	}
	accountName := c.Args().First()
	folderName := c.Args().Get(1)
	recursive := c.Bool("recursive")

	acct, err := app.Accounts.GetByName(c.Context, accountName)
	if err != nil {
		return err
	}

	deleted, err := app.Folders.Delete(c.Context, acct.ID_, recursive, folderName)
	if err != nil {
		return err
	}

	for _, del := range deleted {
		fmt.Println(del.ID, del.Path)
	}

	return nil
}
