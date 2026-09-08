.text
.globl efi_main
efi_main:
mov x0, #7
bl inner
.globl expected_stop
expected_stop:
nop
ret
inner:
mov x0, #0x1234
ret
