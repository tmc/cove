package main

import "github.com/tmc/vz-macos/internal/vmrun"

func macOSDevicePlan(rc vmrun.RunConfig, hc vmrun.HostConfig) (vmrun.DevicePlan, error) {
	plan, err := vmrun.Plan(rc, hc)
	if err != nil {
		return vmrun.DevicePlan{}, err
	}
	if len(plan.Display) == 0 {
		plan.Display = []vmrun.DisplaySpec{{
			Width:  1920,
			Height: 1200,
			PPI:    144,
		}}
	}
	return plan, nil
}
