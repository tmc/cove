.text
.globl efi_main
efi_main:
mov x10, #0x1111
mov x11, #0x2222
mrs x0, pmccntr_el0
mov x2, #10000
loop:
subs x2, x2, #1
b.ne loop
mrs x9, pmccntr_el0
mrs xzr, pmccntr_el0
cmp x9, x0
b.ls failed
mov x4, #0x1111
cmp x10, x4
b.ne failed
mov x4, #0x2222
cmp x11, x4
b.ne failed
brk #99
failed:
brk #100
