// Package version holds the desk build version.
package version

// Version is the desk version; a release build sets it with -ldflags to the tag, and a source build made by
// scripts/fetch-or-build.sh to the manifest's version with a "+src" suffix.
var Version = "dev"
