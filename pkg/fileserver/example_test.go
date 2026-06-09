package fileserver_test

import (
	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/fileserver"
)

// Serve static files from a directory. A request that matches no file falls
// through to the next middleware in the chain, so put fileserver near the end
// to use it as a static-file layer in front of an application handler.
func ExampleNew() {
	s := parapet.New()
	s.Use(fileserver.New("/var/www/public"))
	// s.Use(...) — handles requests for paths with no matching file.
}

// Enable directory listings by setting ListDirectory on the FileServer value
// instead of using the New constructor. With it off (the default), a request
// for a directory falls through to the next handler rather than listing it.
func ExampleFileServer() {
	s := parapet.New()
	s.Use(&fileserver.FileServer{
		Root:          "/srv/downloads",
		ListDirectory: true,
	})
}
