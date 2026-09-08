.text
.p2align 11
.globl entry_vectors
entry_vectors:
.set vector_index, 0
.rept 16
.if (vector_index % 4) == 0
b entry_handler
.else
b frozen
.endif
.space 124
.set vector_index, vector_index+1
.endr
entry_handler:
sub sp, sp, #256
stp x0, x1, [sp, #0]
stp x2, x3, [sp, #16]
stp x4, x5, [sp, #32]
stp x6, x7, [sp, #48]
stp x8, x9, [sp, #64]
stp x10, x11, [sp, #80]
stp x12, x13, [sp, #96]
stp x14, x15, [sp, #112]
stp x16, x17, [sp, #128]
stp x18, x19, [sp, #144]
stp x20, x21, [sp, #160]
stp x22, x23, [sp, #176]
stp x24, x25, [sp, #192]
stp x26, x27, [sp, #208]
stp x28, x29, [sp, #224]
str x30, [sp, #240]
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
add x10, sp, #256
str x10, [x9, #32]
str x30, [x9, #40]
ldr x10, [sp, #72]
str x10, [x9, #64]
ldr x10, [sp, #80]
str x10, [x9, #72]
ldr x10, [sp, #88]
str x10, [x9, #80]
ldr x10, [sp, #96]
str x10, [x9, #88]
adrp x11, entry_saved
add x11, x11, :lo12:entry_saved
ldr x12, [x11, #80]
cbnz x12, check_step
ldr x10, [x9]
ldr x12, [x11, #56]
cmp x10, x12
b.ne frozen
ldr x10, [x9, #8]
mov x12, #0
movk x12, #0x0200, lsl #16
cmp x10, x12
b.ne frozen
b check_mmu
check_step:
adrp x13, trace_meta
add x13, x13, :lo12:trace_meta
ldr x10, [x9, #8]
lsr x10, x10, #26
cmp x10, #0x33
b.eq record_step
mov x10, #3
str x10, [x13, #8]
b check_mmu
record_step:
ldr x10, [x13]
add x10, x10, #1
str x10, [x13]
sub x12, x10, #1
and x12, x12, #127
adrp x14, trace_pcs
add x14, x14, :lo12:trace_pcs
ldr x15, [x9]
str x15, [x14, x12, lsl #3]
cmp x10, #4096
b.eq step_limit
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
ldr x12, [x11, #96]
cmp x15, x12
b.lo image_stop
ldr x12, [x11, #104]
cmp x15, x12
b.hi image_stop
ldr w12, [x15]
str x12, [x13, #16]
lsr w14, w12, #20
mov w15, #0xd51
cmp w14, w15
b.eq system_stop
mov w15, #0xd50
cmp w14, w15
b.eq check_hint
lsr w14, w12, #16
mov w15, #0xd69f
cmp w14, w15
b.eq system_stop
mov w15, #0xd6bf
cmp w14, w15
b.eq system_stop
lsr w14, w12, #24
cmp w14, #0xd4
b.eq system_stop
b check_mmu
check_hint:
mov w15, #0x201f
movk w15, #0xd503, lsl #16
cmp w12, w15
b.eq check_mmu
mov w15, #0x241f
movk w15, #0xd503, lsl #16
cmp w12, w15
b.eq check_mmu
mov w15, #0x245f
movk w15, #0xd503, lsl #16
cmp w12, w15
b.eq check_mmu
mov w15, #0x249f
movk w15, #0xd503, lsl #16
cmp w12, w15
b.eq check_mmu
mov w15, #0x24df
movk w15, #0xd503, lsl #16
cmp w12, w15
b.eq check_mmu
b system_stop
image_stop:
mov x10, #4
str x10, [x13, #8]
str xzr, [x13, #16]
b check_mmu
system_stop:
mov x10, #2
str x10, [x13, #8]
b check_mmu
step_limit:
mov x10, #1
str x10, [x13, #8]
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
adrp x13, trace_meta
add x13, x13, :lo12:trace_meta
ldr x10, [x13, #8]
cbz x10, continue_step
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
and x12, x12, #31
mrs x13, cntvct_el0
adrp x14, supplied_counter
add x14, x14, :lo12:supplied_counter
str x13, [x14]
cmp x12, #31
b.eq counter_discard
str x13, [sp, x12, lsl #3]
counter_discard:
add x10, x10, #4
msr elr_el1, x10
mrs x12, mdscr_el1
orr x12, x12, #1
orr x12, x12, #0x2000
orr x12, x12, #0x8000
msr mdscr_el1, x12
isb
continue_step:
mrs x12, spsr_el1
orr x12, x12, #0x200000
bic x12, x12, #0x200
orr x12, x12, #0xc0
msr spsr_el1, x12
ldp x0, x1, [sp, #0]
ldp x2, x3, [sp, #16]
ldp x4, x5, [sp, #32]
ldp x6, x7, [sp, #48]
ldp x8, x9, [sp, #64]
ldp x10, x11, [sp, #80]
ldp x12, x13, [sp, #96]
ldp x14, x15, [sp, #112]
ldp x16, x17, [sp, #128]
ldp x18, x19, [sp, #144]
ldp x20, x21, [sp, #160]
ldp x22, x23, [sp, #176]
ldp x24, x25, [sp, #192]
ldp x26, x27, [sp, #208]
ldp x28, x29, [sp, #224]
ldr x30, [sp, #240]
add sp, sp, #256
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
