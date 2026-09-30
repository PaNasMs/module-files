package main

import (
	"github.com/PaNasMs/module-sdk/modulehost"
	"net/http"
)

func main() {
	if contentMain() {
		return
	}
	if operationMain() {
		return
	}
	modulehost.Serve("files", func(allowed map[string]bool) http.Handler { return filesHandler(allowed) })
}
