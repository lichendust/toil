# 🗜️ Toil

A simple, no-nonsense, hot-reloading web server for local static site development.

Toil is something I built in a day to solve this problem for myself.  It's never going to be much cleverer than it already is.  You may use it at your own risk.

## What does it do?

It supports regular old static file structures —

	localhost:3456/page/index.html

...but also pretty URLs —

	localhost:3456/page

If those files change on disk, it will hot-reload them. It polls once every second so as not to unduly waste resources.

With appropriate firewall permissions, it can serve as many clients as you like (within reason).

## Installation

With the Go compiler —

	go install github.com/lichendust/toil@latest

## Usage

	toil

Will immediately begin serving and hot-loading the working directory.  It will also, on startup, open your default browser to the `index.html` page it finds there.

	toil path/to/files

You can optionally pass a path, to serve a specific folder.  Toil just changes directories internally and starts normally.

Use Ctrl+C to close it.

## Important Info

Toil expects properly formatted HTML files with at least a `<head>` and `<body>`.  It will insert its client-side hot-reload listener into the `<head>`.  There must *be a head already for this to work*.  Toil is not a smart server and will not solve any incompletely-formatted HTML documents for you.
