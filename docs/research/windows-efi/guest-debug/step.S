.text
.p2align 11
.globl entry_vectors
entry_vectors:
.rept 16
b entry_handler
.space 124
.endr
entry_handler:
stp x9, x10, [sp, #-32]!
stp x11, x12, [sp, #16]
adrp x9, entry_trap
add x9, x9, :lo12:entry_trap
mrs x10, elr_el1
str x10, [x9]
mrs x10, esr_el1
str x10, [x9, #8]
mrs x10, far_el1
str x10, [x9, #16]
mrs x10, spsr_el1
str x10, [x9, #24]
add x10, sp, #32
str x10, [x9, #32]
str x30, [x9, #40]
adrp x11, entry_saved
add x11, x11, :lo12:entry_saved
ldr x12, [x11, #80]
cbnz x12, check_step
ldr x10, [x9]
ldr x12, [x11, #56]
cmp x10, x12
b.ne frozen
ldr x10, [x9, #8]
mov x12, #7
movk x12, #0xf200, lsl #16
cmp x10, x12
b.ne frozen
b check_mmu
check_step:
ldr x10, [x9, #8]
lsr x10, x10, #26
cmp x10, #0x33
b.ne frozen
ldr x10, [x9]
ldr x12, [x11, #56]
add x12, x12, #4
cmp x10, x12
b.ne frozen
check_mmu:
mrs x10, ttbr0_el1
ldr x12, [x11, #24]
cmp x10, x12
b.ne frozen
mrs x10, ttbr1_el1
ldr x12, [x11, #32]
cmp x10, x12
b.ne frozen
mrs x10, tcr_el1
ldr x12, [x11, #40]
cmp x10, x12
b.ne frozen
mrs x10, sctlr_el1
ldr x12, [x11, #48]
cmp x10, x12
b.ne frozen
ldr x10, [x11, #80]
cbz x10, arm_step
ldr x10, [x11, #64]
msr mdscr_el1, x10
isb
ldr x10, [x11, #8]
msr vbar_el1, x10
isb
ldr x10, [x11]
mov sp, x10
ldr x10, [x11, #16]
msr spsr_el1, x10
adr x10, trapped_return
msr elr_el1, x10
eret
arm_step:
mov x10, #1
str x10, [x11, #80]
ldr x10, [x11, #56]
ldr w12, [x11, #72]
str w12, [x10]
dc cvau, x10
dsb ish
ic ivau, x10
dsb ish
isb
mrs x12, mdscr_el1
orr x12, x12, #1
orr x12, x12, #0x2000
orr x12, x12, #0x8000
msr mdscr_el1, x12
isb
mrs x12, spsr_el1
orr x12, x12, #0x200000
bic x12, x12, #0x200
msr spsr_el1, x12
ldp x11, x12, [sp, #16]
ldp x9, x10, [sp], #32
eret
frozen:
b frozen
.globl entry_call
entry_call:
sub sp, sp, #176
str x18, [sp, #160]
stp x19, x20, [sp]
stp x21, x22, [sp, #16]
stp x23, x24, [sp, #32]
stp x25, x26, [sp, #48]
stp x27, x28, [sp, #64]
stp x29, x30, [sp, #80]
stp d8, d9, [sp, #96]
stp d10, d11, [sp, #112]
stp d12, d13, [sp, #128]
stp d14, d15, [sp, #144]
adrp x9, entry_saved
add x9, x9, :lo12:entry_saved
mov x10, sp
str x10, [x9]
mrs x10, vbar_el1
str x10, [x9, #8]
mrs x10, daif
mrs x11, nzcv
orr x10, x10, x11
orr x10, x10, #1
orr x10, x10, #4
str x10, [x9, #16]
mrs x10, ttbr0_el1
str x10, [x9, #24]
mrs x10, ttbr1_el1
str x10, [x9, #32]
mrs x10, tcr_el1
str x10, [x9, #40]
mrs x10, sctlr_el1
str x10, [x9, #48]
mrs x10, mdscr_el1
str x10, [x9, #64]
msr daifset, #15
adrp x10, entry_vectors
add x10, x10, :lo12:entry_vectors
msr vbar_el1, x10
isb
mov x16, x0
mov x0, x1
mov x1, xzr
mov x2, xzr
blr x16
adrp x9, entry_saved
add x9, x9, :lo12:entry_saved
ldr x10, [x9, #64]
msr mdscr_el1, x10
ldr x10, [x9, #8]
msr vbar_el1, x10
isb
ldr x10, [x9, #16]
msr daif, x10
b restore_parent
trapped_return:
mov x0, #1
restore_parent:
ldp x19, x20, [sp]
ldp x21, x22, [sp, #16]
ldp x23, x24, [sp, #32]
ldp x25, x26, [sp, #48]
ldp x27, x28, [sp, #64]
ldp x29, x30, [sp, #80]
ldp d8, d9, [sp, #96]
ldp d10, d11, [sp, #112]
ldp d12, d13, [sp, #128]
ldp d14, d15, [sp, #144]
ldr x18, [sp, #160]
add sp, sp, #176
ret
