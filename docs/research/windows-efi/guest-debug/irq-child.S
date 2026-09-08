.text
.globl efi_main
efi_main:
mov x16, #0x3333
mov x17, #0x4444
mrs x0, cntvct_el0
mrs x1, cntfrq_el0
add x1, x0, x1
msr daifclr, #2
isb
loop:
mrs x0, cntvct_el0
cmp x0, x1
b.lo loop
mov x0, #0
ret
