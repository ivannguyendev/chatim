package main

import (
	"os"

	"github.com/ivannguyendev/chatim/apps/gateway/internal/app"
)

func main() { os.Exit(app.Main(os.Args[1:])) }
