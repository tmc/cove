.text
.globl efi_main
efi_main:
mov x9, #0x1111
mov x10, #0x2222
mov x11, #0x3333
mov x12, #0x4444
mrs x0, pmccntr_el0
mov x0, #4
loop:
subs x0, x0, #1
b.ne loop
mov x1, #0x1111
cmp x9, x1
b.ne failed
mov x1, #0x2222
cmp x10, x1
b.ne failed
mov x1, #0x3333
cmp x11, x1
b.ne failed
mov x1, #0x4444
cmp x12, x1
b.ne failed
.globl expected_stop
expected_stop:
msr daifset, #2
ret
failed:
brk #99
ret
