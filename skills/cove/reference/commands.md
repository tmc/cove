# cove Command Reference

## VM Lifecycle
```
cove install                           # Install macOS from IPSW
cove install -linux                    # Install Ubuntu Linux
cove run                               # Boot VM (resumes from suspend)
cove run -headless                     # Boot without GUI
cove run -no-resume                    # Cold boot (discard suspend state)
cove up -user <name>                   # Install + provision + boot
cove up -user <name> -vzscripts homebrew,golang  # With recipes
```

## Provisioning
```
sudo cove inject -user <name> -password <pass>  # Inject user into disk
sudo cove inject -user <name> -skip-setup-assistant -auto-login
sudo cove inject-agent                 # Inject guest agent only
cove verify                            # Check injection status
```

## Control (running VM)
```
cove ctl status                        # VM state + capabilities
cove ctl screenshot <file.png>         # Capture display
cove ctl type "text"                   # Type text in guest
cove ctl key <keycode> [-modifiers]    # Send key event
cove ctl mouse <x> <y> [-click]        # Mouse event
cove ctl agent-exec -- <cmd> [args]    # Run command as root
cove ctl agent-user-exec -- <cmd>      # Run as logged-in user
cove ctl agent-read <path>             # Read guest file
cove ctl agent-write <path> <data>     # Write guest file
cove ctl agent-cp <host> <guest>       # Copy file to guest
cove ctl agent-info                    # Guest system info
cove ctl pause                         # Pause VM
cove ctl resume                        # Resume VM
cove ctl request-stop                  # ACPI shutdown
```

## Snapshots
```
cove snapshot list                     # List VM state snapshots
cove snapshot save <name>              # Save VM state
cove snapshot restore <name>           # Restore VM state
cove snapshot delete <name>            # Delete snapshot
cove disk-snapshot save <name>         # APFS COW disk snapshot
cove disk-snapshot list                # List disk snapshots
cove disk-snapshot restore <name>      # Restore disk snapshot
cove disk-snapshot delete <name>       # Delete disk snapshot
```

## VZScript
```
cove vzscript list                     # List built-in recipes
cove vzscript show <name>              # Print recipe contents
cove vzscript run <name> [name...]     # Run recipes (deps resolved)
cove vzscript run ./custom.vzscript    # Run custom script
```

## SIP Management
```
cove sip status                        # Query SIP status via agent
cove sip enable                        # Show enable instructions
cove sip disable                       # Show disable instructions
cove sip disable-auto -user <u> -password <p> -confirm  # Automated
cove sip create-disk                   # Create recovery tools disk
```

## Shared Folders
```
cove shared-folder add <path>          # Add persistent folder
cove shared-folder remove <path>       # Remove folder
cove shared-folder list                # List configured folders
cove run -share <path> [-share <path:ro>]  # Mount at runtime
```

## VM Management
```
cove vm list                           # List VMs
cove clone <src> <dst>                 # Clone VM
cove template save <name>              # Save as template
cove template list                     # List templates
cove gc                                # Clean up unused VMs
```

## Display & Network
```
cove run -display 4k                   # 4K display preset
cove run -display 1920x1080            # Custom resolution
cove run -network nat                  # NAT (default)
cove run -network bridged:en0          # Bridged
cove run -network none                 # No network
cove run -rosetta                      # Enable Rosetta (Linux)
```
