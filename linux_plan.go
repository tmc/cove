package main

import "github.com/tmc/vz-macos/internal/vmrun"

func linuxDevicePlan(rc vmrun.RunConfig, hc vmrun.HostConfig) (vmrun.DevicePlan, error) {
	plan, err := vmrun.Plan(rc, hc)
	if err != nil {
		return vmrun.DevicePlan{}, err
	}
	if len(plan.Display) == 0 {
		plan.Display = []vmrun.DisplaySpec{{
			Width:  defaultWindowWidth,
			Height: defaultWindowHeight,
			PPI:    144,
		}}
	}
	return plan, nil
}
