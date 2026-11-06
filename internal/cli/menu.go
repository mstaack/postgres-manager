package cli

import (
	"context"
	"errors"

	"github.com/mstaack/postgres-manager/internal/ui"
)

// loop shows a menu until back/quit/EOF. A failing action prints its error and the
// menu continues; ESC inside an action cancels just that action.
func (a *App) loop(ctx context.Context, show func() error, items []ui.MenuItem, actions map[string]func() error) error {
	for {
		if show != nil {
			if err := show(); err != nil {
				a.UI.Error(err)
			}
			a.UI.Println()
		}
		choice, err := a.UI.Menu(items)
		if err != nil {
			return nil
		}
		switch choice {
		case "b", "B", "q", "Q", "":
			return nil
		}
		act, ok := actions[choice]
		if !ok {
			a.UI.Warn("Unknown choice")
			continue
		}
		if err := act(); err != nil {
			if errors.Is(err, ui.ErrCancelled) {
				a.UI.Info("Cancelled — back to the menu")
			} else {
				a.UI.Error(err)
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

func (a *App) mainMenu(ctx context.Context) error {
	a.showStatus(ctx)
	if !a.HasCluster {
		a.UI.Header("No PostgreSQL cluster")
		return a.loop(ctx, nil, []ui.MenuItem{
			{Key: "1", Label: "Install PostgreSQL", Desc: "pick a major version; creates and starts a cluster"},
			{Key: "q", Label: "Quit"},
		}, map[string]func() error{
			"1": func() error { return a.install(ctx, "") },
		})
	}
	title := func() error { a.UI.Header("PostgreSQL " + a.PG.C.ID()); return nil }
	return a.loop(ctx, title, []ui.MenuItem{
		{Key: "1", Label: "Databases", Desc: "list, create and drop databases"},
		{Key: "2", Label: "Users & access", Desc: "create users, read-only access, passwords, drop users"},
		{Key: "3", Label: "Extensions", Desc: "PostGIS, pgvector, TimescaleDB, pg_cron, pg_stat_statements"},
		{Key: "4", Label: "Backups & restore", Desc: "pgBackRest backups, point-in-time restore, restore drill"},
		{Key: "5", Label: "PostgreSQL versions", Desc: "install another major version, upgrade this cluster"},
		{Key: "s", Label: "Status", Desc: "refresh the dashboard"},
		{Key: "q", Label: "Quit"},
	}, map[string]func() error{
		"1": func() error { return a.dbMenu(ctx) },
		"2": func() error { return a.userMenu(ctx) },
		"3": func() error { return a.extensions(ctx) },
		"4": func() error { return a.backupsMenu(ctx) },
		"5": func() error { return a.versionsMenu(ctx) },
		"s": func() error { a.showStatus(ctx); return nil },
		"S": func() error { a.showStatus(ctx); return nil },
	})
}

func (a *App) versionsMenu(ctx context.Context) error {
	return a.loop(ctx, func() error { a.UI.Header("PostgreSQL versions · running " + a.PG.C.ID()); return nil }, []ui.MenuItem{
		{Key: "1", Label: "Install a major version", Desc: "adds a new cluster next to this one"},
		{Key: "2", Label: "Upgrade this cluster", Desc: "pg_upgradecluster --link, then stanza-upgrade and a full backup"},
		{Key: "b", Label: "Back"},
	}, map[string]func() error{
		"1": func() error { return a.install(ctx, "") },
		"2": func() error { return a.upgrade(ctx, "", false) },
	})
}

func (a *App) backupsMenu(ctx context.Context) error {
	for {
		var items []ui.MenuItem
		if a.Configured {
			a.UI.Header("Backups & restore · " + a.repoLabels())
			items = []ui.MenuItem{
				{Key: "1", Label: "List backups", Desc: "backup sets, sizes and the point-in-time recovery window"},
				{Key: "2", Label: "Run backup now", Desc: "full = everything, diff = changes since the last full, incr = since the last backup"},
				{Key: "3", Label: "Restore / point in time", Desc: "whole cluster or one database, to any second in the window"},
				{Key: "4", Label: "Restore drill", Desc: "proves the backups work: restores the latest into a temp dir, starts it, compares, removes it"},
				{Key: "5", Label: "Check archiving", Desc: "pgBackRest check: WAL archiving and repository access"},
				{Key: "6", Label: "Settings", Desc: "schedule, retention, RPO, performance, repositories, heartbeat, passphrase"},
				{Key: "7", Label: "Re-run setup", Desc: "reconfigure pgBackRest and WAL archiving from scratch"},
				{Key: "b", Label: "Back"},
			}
		} else {
			a.UI.Header("Backups & restore · not configured")
			items = []ui.MenuItem{
				{Key: "1", Label: "Set up backups", Desc: "pgBackRest + continuous WAL archiving to Cloudflare R2 (EU), a local directory or S3"},
				{Key: "b", Label: "Back"},
			}
		}
		choice, err := a.UI.Menu(items)
		if err != nil {
			return nil
		}
		if !a.Configured && choice == "1" {
			choice = "7"
		}
		var act func() error
		switch choice {
		case "1":
			act = func() error { return a.list(ctx) }
		case "2":
			act = func() error {
				t, err := a.UI.Ask("Type (full/diff/incr)", "diff")
				if err != nil {
					return err
				}
				return a.backupNow(ctx, t)
			}
		case "3":
			act = func() error { return a.restore(ctx, restoreOpts{}) }
		case "4":
			act = func() error { return a.verify(ctx, false) }
		case "5":
			act = func() error { return a.check(ctx) }
		case "6":
			act = func() error { return a.edit(ctx) }
		case "7":
			act = func() error { return a.setup(ctx) }
		case "b", "B", "q", "Q", "":
			return nil
		default:
			a.UI.Warn("Unknown choice")
			continue
		}
		if err := act(); err != nil {
			if errors.Is(err, ui.ErrCancelled) {
				a.UI.Info("Cancelled — back to the menu")
			} else {
				a.UI.Error(err)
			}
		}
	}
}
