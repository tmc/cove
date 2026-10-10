package guest

import (
	"context"
	"fmt"
)

func ExampleNewSession() {
	session, err := NewSession("/path/to/running-vm/control.sock")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer session.Close()
	fmt.Println("session owns client requests")
	// Output: session owns client requests
}

func ExampleSession_Close() {
	session, _ := NewSession("/path/to/running-vm/control.sock")
	fmt.Println(session.Close())
	fmt.Println(session.Close())
	// Output:
	// <nil>
	// <nil>
}

func ExampleSession_Ready() {
	session, _ := NewSession("/path/to/running-vm/control.sock")
	defer session.Close()
	status, err := session.Ready(context.Background())
	if err != nil {
		fmt.Println("transport unavailable")
		return
	}
	fmt.Println(status.State)
	// Output: transport unavailable
}

func ExampleSession_Inspect() {
	session, _ := NewSession("/path/to/running-vm/control.sock")
	defer session.Close()
	observation, err := session.Inspect(context.Background(), Query{PID: 123, MaxNodes: 100})
	if err != nil {
		fmt.Println("transport unavailable")
		return
	}
	fmt.Println(observation.Status.State, observation.Truncated)
	// Output: transport unavailable
}

func ExampleSession_Find() {
	session, _ := NewSession("/path/to/running-vm/control.sock")
	defer session.Close()
	observation, err := session.Find(context.Background(), Query{PID: 123, Role: "AXButton", Label: "Save"})
	if err != nil {
		fmt.Println("transport unavailable")
		return
	}
	fmt.Println(observation.MatchState)
	// Output: transport unavailable
}

func ExampleSession_Status() {
	session, _ := NewSession("/path/to/running-vm/control.sock")
	defer session.Close()
	status, err := session.Status(context.Background(), Query{ExpectedGeneration: "previous-session"})
	if err != nil {
		fmt.Println("transport unavailable")
		return
	}
	fmt.Println(status.State)
	// Output: transport unavailable
}
