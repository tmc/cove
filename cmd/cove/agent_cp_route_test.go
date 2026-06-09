package main

import (
	"testing"

	agentstate "github.com/tmc/cove/internal/agent"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestCopyRoute(t *testing.T) {
	oldLinux := linuxMode
	defer func() { linuxMode = oldLinux }()

	for _, tc := range []struct {
		name      string
		linux     bool
		route     controlpb.AgentRoute
		guestPath string
		want      agentstate.Route
	}{
		{name: "auto system path -> daemon", route: controlpb.AgentRoute_AGENT_ROUTE_AUTO, guestPath: "/tmp/f", want: agentstate.RouteDaemon},
		{name: "auto volumes path -> user", route: controlpb.AgentRoute_AGENT_ROUTE_AUTO, guestPath: "/Volumes/models/f", want: agentstate.RouteUser},
		{name: "auto downloads -> user", route: controlpb.AgentRoute_AGENT_ROUTE_AUTO, guestPath: "/Users/me/Downloads/f", want: agentstate.RouteUser},
		{name: "forced daemon overrides user path", route: controlpb.AgentRoute_AGENT_ROUTE_DAEMON, guestPath: "/Volumes/models/f", want: agentstate.RouteDaemon},
		{name: "forced user overrides system path", route: controlpb.AgentRoute_AGENT_ROUTE_USER, guestPath: "/tmp/f", want: agentstate.RouteUser},
		{name: "linux auto -> daemon", linux: true, route: controlpb.AgentRoute_AGENT_ROUTE_AUTO, guestPath: "/Volumes/models/f", want: agentstate.RouteDaemon},
		{name: "linux forced user -> daemon", linux: true, route: controlpb.AgentRoute_AGENT_ROUTE_USER, guestPath: "/Volumes/models/f", want: agentstate.RouteDaemon},
	} {
		t.Run(tc.name, func(t *testing.T) {
			linuxMode = tc.linux
			got := copyRoute(&controlpb.AgentCopyCommand{Route: tc.route, GuestPath: tc.guestPath})
			if got != tc.want {
				t.Fatalf("copyRoute(%q, %v, linux=%v) = %v, want %v", tc.guestPath, tc.route, tc.linux, got, tc.want)
			}
		})
	}
}
