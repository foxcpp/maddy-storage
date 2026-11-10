package storagecli

import (
	"github.com/urfave/cli/v2"
)

func (a AppProvider) migrationsUp(c *cli.Context) error {
	app, err := a(c)
	if err != nil {
		return err
	}

	return app.DB.MigrationsUp(c.Context)
}

func (a AppProvider) migrationsDown(c *cli.Context) error {
	app, err := a(c)
	if err != nil {
		return err
	}

	return app.DB.MigrationsDown(c.Context)
}
