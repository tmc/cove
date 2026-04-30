---
title: License and Virtualization Limits
---
# License and Virtualization Limits

This page is a product disclosure, not legal advice. Check the license text for
the exact version of each project and the Apple SLA for the macOS version you
run before relying on this in a procurement, compliance, or hosted-service
decision.

## Apple macOS virtualization

macOS guests are governed by Apple's macOS Software License Agreement, not just
by the license of the VM tool. The current
[macOS Tahoe 26 SLA](https://www.apple.com/legal/sla/docs/macOSTahoe.pdf)
section 2B(iii) permits up to two additional virtualized macOS copies or
instances on each Apple-branded computer you own or control, for the listed
development, testing, macOS Server, or personal non-commercial purposes. Except
as separately permitted by Apple, it also excludes service bureau, time-sharing,
terminal sharing, relay service, and similar services.

cove does not bypass or expand those Apple terms. For macOS guests, fleet
capacity is hardware capacity plus whatever rights Apple grants for the macOS
version and use case. Read the applicable SLA from Apple's current license page:
<https://www.apple.com/legal/sla/>.

Linux guests do not have the Apple macOS guest limit, but the Linux
distribution, package, image, and registry terms still apply.

## Project license comparison

| Tool | Project license | Fleet or use trigger to notice |
|---|---|---|
| cove | MIT | No cove license fee or project cap; Apple SLA still applies to macOS guests. |
| Lume | MIT | No Lume license fee or project cap in the current public repo; Apple SLA still applies to macOS guests. |
| Tart | Fair Source 0.9 | Tart's published licensing page says organizations above the free tier need a paid license; the free tier is 100 CPU cores. |
| Orchard | Fair Source 0.9 | Tart's published licensing page says Orchard's free tier is 4 workers/hosts. |
| tart-guest-agent | FSL-1.1-Apache-2.0 | FSL projects have a competing-use restriction during the delay period, then convert to Apache-2.0 under their license terms. |

Primary sources:

- Apple Software License Agreements: <https://www.apple.com/legal/sla/>
- macOS Tahoe 26 SLA: <https://www.apple.com/legal/sla/docs/macOSTahoe.pdf>
- Cua/Lume license: <https://raw.githubusercontent.com/trycua/cua/main/LICENSE.md>
- Tart and Orchard licensing: <https://tart.run/licensing/>
- tart-guest-agent license: <https://raw.githubusercontent.com/cirruslabs/tart-guest-agent/main/LICENSE>

## Release-docs rules

- Do not describe cove as exempt from Apple's macOS guest limits.
- Do not describe Tart as abandoned or unmaintained. The release claim should be
  about license arithmetic unless a fresh maintenance review says otherwise.
- Do not claim APFS `clonefile` is exclusive to cove. The cove claim is named
  multi-snapshot fork/restore plus the automation stack around it.
- Keep `cove build` documented as dry-run planning until the VM execution path
  ships.
- Keep the trademark warning visible: do not publish a public `cove` registry or
  signed agentkit image channel until counsel clears the name or a rename lands.
