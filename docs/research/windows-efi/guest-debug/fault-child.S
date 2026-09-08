.text
.globl efi_main
efi_main:
mrs x9, pmccntr_el0
mov x0, #0
ret
