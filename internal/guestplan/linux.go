package guestplan

import "github.com/tmc/cove/internal/vmrun"

func Linux(rc vmrun.RunConfig, hc vmrun.HostConfig) (vmrun.DevicePlan, error) {
	plan, err := vmrun.Plan(rc, hc)
	if err != nil {
		return vmrun.DevicePlan{}, err
	}
	if len(plan.Display) == 0 {
		plan.Display = []vmrun.DisplaySpec{{
			Width:  1024,
			Height: 768,
			PPI:    144,
		}}
	}
	return plan, nil
}
