package restore

import (
	"context"
	"net/http"
)

// RecoverySigningRequest derives a recovery OS root ticket request from the
// selected build identity. Components identifies the recovery images whose
// digests must be present in the returned ticket. Recovery selection excludes
// boot-chain and restore-ramdisk components; it does not use AP selection's
// trusted-only fallback or FTAB and Cryptex exclusions. Inputs are copied.
func RecoverySigningRequest(identity map[string]any, device SigningDevice, components []string) (map[string]any, TicketRequirements, error) {
	return apSigningRequest(identity, device, components, true)
}

// SignRecovery requests and checks a recovery OS root ticket over the same
// trusted HTTPS transport as SignAP. It does not authenticate the ticket's
// signature or perform a restore. Components must belong to the recovery build.
func SignRecovery(ctx context.Context, client *http.Client, identity map[string]any, device SigningDevice, components []string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	request, want, err := RecoverySigningRequest(identity, device, components)
	if err != nil {
		return nil, err
	}
	return signRequest(ctx, client, request, want)
}

func skipRecoveryComponent(name string) bool {
	switch name {
	case "BasebandFirmware", "SE,UpdatePayload", "BaseSystem", "ANS",
		"Ap,AudioBootChime", "Ap,CIO", "Ap,RestoreCIO", "Ap,RestoreTMU", "Ap,TMU",
		"Ap,rOSLogo1", "Ap,rOSLogo2", "AppleLogo", "DCP", "LLB", "RecoveryMode",
		"RestoreANS", "RestoreDCP", "RestoreDeviceTree", "RestoreKernelCache",
		"RestoreLogo", "RestoreRamDisk", "RestoreSEP", "SEP", "ftap", "ftsp",
		"iBEC", "iBSS", "rfta", "rfts", "Diags":
		return true
	}
	return false
}
