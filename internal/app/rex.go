package app

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// rexPanelLabel labels the pane of the tackroom panel; a later run finds the
// panel by it.
const rexPanelLabel = "tackroom"

// rexPanelColumns is the width the panel aims for: the dash's job rows keep
// their state column and its keys line fits.
const rexPanelColumns = 62

// rexNode is a node of a Rex window layout: a block, or a split of two nodes.
// rect is [x, y, w, h] as fractions of the window.
type rexNode struct {
	Rect    []float64 `json:"rect"`
	BlockID string    `json:"block_id"`
	Split   *struct {
		Before *rexNode `json:"before"`
		After  *rexNode `json:"after"`
	} `json:"split"`
}

// leaf is the node of block, or with block empty or absent, the first block.
func (n *rexNode) leaf(block string) *rexNode {
	if n == nil {
		return nil
	}
	if n.Split == nil {
		if n.BlockID != "" && (block == "" || n.BlockID == block) {
			return n
		}
		return nil
	}
	if l := n.Split.Before.leaf(block); l != nil {
		return l
	}
	return n.Split.After.leaf(block)
}

// runRexCommand toggles the tackroom panel in the Rex session it runs in: a
// floating pane at the right edge of the window, at full height, running
// `tackroom dash`. The first run opens it with the focus, the next hides it and
// the one after shows it again; hidden, the dash keeps running. A panel in
// another window of the session moves to this one, so a session has one panel.
// Sessions, windows and their labels stay as they are. It is the optional Rex
// terminal integration; an absent Rex, or a run outside Rex, is an error.
func runRexCommand(args []string) error {
	fs := flag.NewFlagSet("rex", flag.ContinueOnError)
	config := fs.String("config", "", "Path to tackroom YAML config")
	dryRun := fs.Bool("dry-run", false, "print the rex command that would change the panel without running it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: tackroom rex [--config PATH] [--dry-run]")
	}

	rex, err := exec.LookPath("rex")
	if err != nil {
		return fmt.Errorf("rex not found on PATH; install the Rex terminal first")
	}
	// Rex sets both in every pane it runs, and the palette action passes the
	// ones it was run from.
	session, here := os.Getenv("REX_SESSION"), os.Getenv("REX_BLOCK")
	if session == "" {
		return errors.New("tackroom rex toggles a panel in the Rex session it runs in: run it in a Rex pane, or from Rex's command palette with rex/tackroom.lua")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	// --config is a config file, as for every other command; the dash runs in
	// its folder.
	configPath, err := resolveConfigPath(*config, home)
	if err != nil {
		return err
	}
	root := filepath.Dir(configPath)

	// The panel runs this tackroom, by the path it was started as, so an older
	// tackroom earlier on the login shell's PATH cannot answer for it. The path
	// is not resolved through symlinks, so a package manager's link stays valid
	// across upgrades.
	self, err := os.Executable()
	if err != nil {
		self = "tackroom"
	}
	dash := []string{self, "dash"}
	if *config != "" {
		dash = append(dash, "--config", configPath)
	}

	// line is a rex command as a shell would take it: a word with anything a
	// shell reads is quoted.
	line := func(args []string) string {
		words := append([]string{rex}, args...)
		for i, w := range words {
			if w == "" || strings.Trim(w, plainShellWord) != "" {
				words[i] = shellQuote(w)
			}
		}
		return strings.Join(words, " ")
	}
	// run runs a rex command and returns what it printed. Under --dry-run a
	// command that changes Rex is printed instead; the ones that only read run
	// either way, since what changes depends on them.
	run := func(change bool, args ...string) ([]byte, error) {
		if change && *dryRun {
			fmt.Println(line(args))
			return nil, nil
		}
		out, err := exec.Command(rex, args...).Output()
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				out = append(out, exit.Stderr...)
			}
			return nil, fmt.Errorf("%s: %w\n%s", line(args), err, out)
		}
		return out, nil
	}
	call := func(change bool, method string, in map[string]any) error {
		body, err := json.Marshal(in)
		if err != nil {
			return err
		}
		_, err = run(change, "api", "call", "-s", session, method, string(body))
		return err
	}

	out, err := run(false, "api", "call", "-s", session, "session.view")
	if err != nil {
		return err
	}
	var view struct {
		ActiveWindowID string `json:"active_window_id"`
		Windows        []struct {
			WindowID string `json:"window_id"`
			Layers   []struct {
				LayerID string  `json:"layer_id"`
				Kind    string  `json:"kind"`
				State   string  `json:"state"`
				Layout  rexNode `json:"layout"`
				Blocks  []struct {
					BlockID string `json:"block_id"`
					Label   string `json:"label"`
				} `json:"blocks"`
			} `json:"layers"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(out, &view); err != nil {
		return fmt.Errorf("rex session.view: unexpected output: %s", out)
	}

	// The panel goes into the window of the pane it was run from, else the
	// window the session shows. An existing panel is a floating layer with a
	// pane labelled tackroom, in any window.
	window := view.ActiveWindowID
	panel, panelState, panelWindow := "", "", ""
	for _, w := range view.Windows {
		for _, l := range w.Layers {
			for _, b := range l.Blocks {
				if here != "" && b.BlockID == here {
					window = w.WindowID
				}
				if l.Kind == "floating" && b.Label == rexPanelLabel && panel == "" {
					panel, panelState, panelWindow = l.LayerID, l.State, w.WindowID
				}
			}
		}
	}
	if window == "" {
		return errors.New("rex: the session has no window to put the panel in")
	}
	var tiled *rexNode
	for _, w := range view.Windows {
		for i, l := range w.Layers {
			if w.WindowID == window && l.Kind == "tiled" {
				tiled = &w.Layers[i].Layout
			}
		}
	}

	if panel != "" && panelWindow == window {
		if panelState == "minimized" {
			if err := call(true, "session.set_layer_state", map[string]any{"layer_id": panel, "state": "normal", "focus": true}); err != nil {
				return err
			}
			if !*dryRun {
				fmt.Println("Showed the tackroom panel.")
			}
			return nil
		}
		if err := call(true, "session.set_layer_state", map[string]any{"layer_id": panel, "state": "minimized"}); err != nil {
			return err
		}
		if !*dryRun {
			fmt.Println("Hid the tackroom panel; the dash keeps running.")
		}
		return nil
	}
	if panel != "" {
		if err := call(true, "session.close_layer", map[string]any{"layer_id": panel}); err != nil {
			return err
		}
	}

	// The panel is rexPanelColumns wide, within 30% to 60% of the window, and
	// 40% when the window's width in columns cannot be read. A floating layer
	// leaves the panes under it as they are, so nothing reflows when it shows.
	width := 0.4
	leaf := tiled.leaf(here)
	if leaf == nil {
		leaf = tiled.leaf("")
	}
	if leaf != nil && len(leaf.Rect) == 4 && leaf.Rect[2] > 0 {
		if sized, err := run(false, "block", "call", "-s", session, "-b", leaf.BlockID, "size", "{}"); err == nil {
			var size struct {
				Columns float64 `json:"columns"`
			}
			if json.Unmarshal(sized, &size) == nil && size.Columns > 0 {
				width = math.Round(min(max(rexPanelColumns/(size.Columns/leaf.Rect[2]), 0.3), 0.6)*100) / 100
			}
		}
	}
	// Quitting the dash with q ends its pane, and with it the panel.
	if err := call(true, "session.new_layer", map[string]any{
		"window_id": window,
		"bounds":    []float64{math.Round((1-width)*100) / 100, 0, width, 1},
		"focus":     true,
		"layout": map[string]any{"block": map[string]any{
			"flavor":  "com.superlogical.terminal.shell",
			"label":   rexPanelLabel,
			"options": map[string]any{"command": dash, "cwd": root},
		}},
	}); err != nil {
		return err
	}
	if !*dryRun {
		fmt.Println("Opened the tackroom panel; run tackroom rex again to hide it, or press q in it to close it.")
	}
	return nil
}
