// verbose.go - shared verbose-logging toggle for the controlserver
// package. Set from package main during startup so moved code retains
// the same behavior as when it lived in main.
package controlserver

// Verbose enables debug logging in the iTerm2 proxy and port-forward
// relays. Default false.
var Verbose bool
