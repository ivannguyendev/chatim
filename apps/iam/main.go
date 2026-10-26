package main

import (
	"os"

	"github.com/ivannguyendev/chatim/apps/iam/internal/app"
)

func main() { os.Exit(app.Main(os.Args[1:])) }
