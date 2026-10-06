package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

const usage = `usage: corecli <command> [flags]

commands:
  create-room        create a room through any live core
  send               send one message to the core serving the room
  history            read one history page of a room
  edit               edit a message (-base: the version you saw)
  delete             delete a message for everyone (-base: the version you saw)
  hide               hide a message for the caller only
  clear              hide the history up to a seq for the caller only
  edits              list the edit history of a message
  react              set or change the caller's reaction on a message (-emoji "" removes it)
  reaction-settings  print the reaction emojis the core accepts, as JSON
  pin                pin a message in its room
  unpin              unpin a message
  watch              print live events of a room from NATS
  slots              show how live cores share the slots
  e2e                end-to-end scenario steps: setup, send, change, react-pin, check

run "corecli <command> -h" for the flags of a command`

type command func(ctx context.Context, args []string) error

func main() { os.Exit(realMain(os.Args[1:])) }

func realMain(args []string) int {
	commands := map[string]command{
		"create-room":       createRoomCmd,
		"send":              sendCmd,
		"history":           historyCmd,
		"edit":              editCmd,
		"delete":            deleteCmd,
		"hide":              hideCmd,
		"clear":             clearCmd,
		"edits":             editsCmd,
		"react":             reactCmd,
		"reaction-settings": reactionSettingsCmd,
		"pin":               pinCmd,
		"unpin":             unpinCmd,
		"watch":             watchCmd,
		"slots":             slotsCmd,
		"e2e":               e2eCmd,
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cmd(ctx, args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 2
		}
		fmt.Fprintf(os.Stderr, "corecli %s: %v\n", args[0], err)
		return 1
	}
	return 0
}
