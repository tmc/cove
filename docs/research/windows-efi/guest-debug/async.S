.text
.p2align 11
.globl entry_vectors
entry_vectors:
.set vector_index, 0
.rept 16
stp x16, x17, [sp, #-16]!
mov x16, #vector_index
b entry_handler
.space 116
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
str xzr, [sp, #248]
mov x15, x16
ldr x16, [sp, #256]
str x16, [sp, #128]
ldr x17, [sp, #264]
str x17, [sp, #136]
adrp x9, emu_vector
add x9, x9, :lo12:emu_vector
str x15, [x9]
adrp x9, emu_gp
add x9, x9, :lo12:emu_gp
mov x10, sp
mov x11, #16
copy_gp:
ldp x12, x13, [x10], #16
stp x12, x13, [x9], #16
subs x11, x11, #1
b.ne copy_gp
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
add x10, sp, #272
str x10, [x9, #32]
ldr x10, [sp, #240]
str x10, [x9, #40]
adrp x11, entry_saved
add x11, x11, :lo12:entry_saved
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
adrp x12, emu_vector
add x12, x12, :lo12:emu_vector
ldr x10, [x12]
ands x10, x10, #3
b.ne async_stop
ldr x10, [x9, #8]
mov x12, #0x2000000
cmp x10, x12
b.ne different_fault
ldr x15, [x9]
adrp x13, emu_ranges
add x13, x13, :lo12:emu_ranges
adrp x14, emu_nrange
add x14, x14, :lo12:emu_nrange
ldr x14, [x14]
range_loop:
cbz x14, outside_image
ldp x10, x12, [x13], #16
sub x14, x14, #1
cmp x15, x10
b.lo range_loop
cmp x15, x12
b.hi range_loop
ldr w10, [x15]
mov w12, #0x9d00
movk w12, #0xd53b, lsl #16
and w13, w10, #0xffffffe0
cmp w13, w12
b.ne different_fault
adrp x12, emu_count
add x12, x12, :lo12:emu_count
ldr x13, [x12]
cmp x13, #64
b.hs limit_stop
adrp x14, emu_pcs
add x14, x14, :lo12:emu_pcs
str x15, [x14, x13, lsl #3]
add x13, x13, #1
str x13, [x12]
and x10, x10, #31
mrs x14, cntvct_el0
cmp x10, #31
b.eq discard_result
str x14, [sp, x10, lsl #3]
discard_result:
add x15, x15, #4
msr elr_el1, x15
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
add sp, sp, #272
eret
async_stop:
mov x10, #4
b recover
limit_stop:
mov x10, #1
b recover
outside_image:
mov x10, #3
b recover
different_fault:
mov x10, #2
recover:
adrp x12, emu_reason
add x12, x12, :lo12:emu_reason
str x10, [x12]
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
