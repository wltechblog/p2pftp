package main

import "embed"

//go:embed static/index.html static/app.js
var staticFiles embed.FS
