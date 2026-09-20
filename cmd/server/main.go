package main

import (
	"net/http"
	"github.com/PaNasMs/module-sdk/modulehost"
)

func main() {
	modulehost.Serve("files", func(allowed map[string]bool) http.Handler { return filesHandler(allowed) })
}
