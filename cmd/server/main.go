package main

import (
	"net/http"
	"github.com/OstojaOS/module-sdk/modulehost"
)

func main() {
	modulehost.Serve("files", func(allowed map[string]bool) http.Handler { return filesHandler(allowed) })
}
