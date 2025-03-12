package main

import "embed"

//go:embed static/index.html static/app.js static/js/*
var staticFiles embed.FS
