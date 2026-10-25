package main

import (
	"os"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/app"
)

func main() { os.Exit(app.Main(os.Args[1:])) }
