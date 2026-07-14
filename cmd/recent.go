package cmd

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"segura-cli/internal/config"
	"segura-cli/internal/picker"
	"segura-cli/internal/recent"
)

var recentList bool

// recentCmd shows recent/favorite targets and lets you reconnect quickly.
var recentCmd = &cobra.Command{
	Use:   "recent",
	Short: "Reconnect to a recent or favorite target",
	Long: `Shows your recently-used and favorite connection targets.

With no flag it opens an interactive picker limited to those targets; select one
to reconnect. Use --list to only print them.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		store, err := recent.Load()
		if err != nil {
			return err
		}
		entries := store.Sorted()
		if len(entries) == 0 {
			fmt.Println("No recent connections yet. Use 'segura connect' to get started.")
			return nil
		}

		if recentList {
			printRecent(entries)
			return nil
		}

		items := make([]picker.Item, len(entries))
		for i, e := range entries {
			items[i] = picker.Item{
				Username: e.Username, Device: e.Device, IP: e.IP,
				Favorite: e.Favorite, LastUsed: e.LastUsed, Count: e.Count,
			}
		}
		item, err := picker.Select(items, "")
		if err != nil {
			if errors.Is(err, picker.ErrCancelled) {
				fmt.Println("Cancelled.")
				return nil
			}
			return err
		}

		cfg, err := config.Load(envFile)
		if err != nil {
			return fmt.Errorf("configuration error: %w", err)
		}
		return runConnection(cfg, item.Username, item.Device, item.IP)
	},
}

func printRecent(entries []recent.Entry) {
	for _, e := range entries {
		marker := " "
		if e.Favorite {
			marker = "★"
		} else if e.LastUsed > 0 {
			marker = "•"
		}
		when := ""
		if e.LastUsed > 0 {
			when = time.Unix(e.LastUsed, 0).Format("2006-01-02 15:04")
		}
		fmt.Printf("%s  %-30s %-16s %s\n", marker, e.Username+"@"+e.Device, e.IP, when)
	}
}

// favCmd manages favorites.
var favCmd = &cobra.Command{
	Use:   "fav",
	Short: "Manage favorite connection targets",
	Long: `Manage favorite targets shown first in the picker.

  segura fav                 list favorites
  segura fav add cred@device pin a target as favorite
  segura fav rm  cred@device unpin a target`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		store, err := recent.Load()
		if err != nil {
			return err
		}

		if len(args) == 0 {
			var favs []recent.Entry
			for _, e := range store.Sorted() {
				if e.Favorite {
					favs = append(favs, e)
				}
			}
			if len(favs) == 0 {
				fmt.Println("No favorites yet. Add one with 'segura fav add cred@device'.")
				return nil
			}
			printRecent(favs)
			return nil
		}

		action := strings.ToLower(args[0])
		if len(args) < 2 {
			return fmt.Errorf("usage: segura fav %s cred@device", action)
		}
		cred, device, err := parseTarget(args[1])
		if err != nil {
			return err
		}

		switch action {
		case "add":
			store.SetFavorite(cred, device, "", true)
			if err := store.Save(); err != nil {
				return err
			}
			fmt.Printf("★ Added %s@%s to favorites\n", cred, device)
		case "rm", "remove", "del":
			store.SetFavorite(cred, device, "", false)
			if err := store.Save(); err != nil {
				return err
			}
			fmt.Printf("Removed %s@%s from favorites\n", cred, device)
		default:
			return fmt.Errorf("unknown action %q (use add or rm)", action)
		}
		return nil
	},
}

func init() {
	recentCmd.Flags().BoolVar(&recentList, "list", false, "Only print recent targets (don't open the picker)")
	rootCmd.AddCommand(recentCmd)
	rootCmd.AddCommand(favCmd)
}
