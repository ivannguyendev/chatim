package main

import (
	"os"

	"github.com/ivannguyendev/chatim/apps/core/internal/app"
)

func main() { os.Exit(app.Main(os.Args[1:])) }
