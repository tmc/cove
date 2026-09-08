
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

.globl read_dfr0
read_dfr0:
mrs x0, id_aa64dfr0_el1
ret
.globl read_cntfrq
read_cntfrq:
mrs x0, cntfrq_el0
ret
.globl read_cntvct
read_cntvct:
mrs x0, cntvct_el0
ret
