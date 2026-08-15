//go:build !darwin

package main

func (s *ControlServer) pgDisplayStatus() DisplayStatus {
	return DisplayStatus{Reason: "pgdisplay unavailable on this platform"}
}
