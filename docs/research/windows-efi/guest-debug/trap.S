
.text
.p2align 11
.globl exception_vectors
exception_vectors:
.rept 16
b exception_handler
.space 124
.endr
exception_handler:
stp x16, x17, [sp, #-16]!
adrp x16, trap_state
add x16, x16, :lo12:trap_state
mrs x17, elr_el1
str x17, [x16]
add x17, x17, #4
msr elr_el1, x17
mrs x17, esr_el1
str x17, [x16, #8]
mrs x17, far_el1
str x17, [x16, #16]
mrs x17, spsr_el1
str x17, [x16, #24]
add x17, sp, #16
str x17, [x16, #32]
ldp x16, x17, [sp], #16
eret
.globl probe_brk
probe_brk:
mov x16, #0x1234
mov x17, #0x5678
.globl trap_site
trap_site:
brk #7
adrp x0, trap_state
add x0, x0, :lo12:trap_state
stp x16, x17, [x0, #40]
ret
