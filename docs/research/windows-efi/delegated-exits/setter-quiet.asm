sub_223fa4220:
0x223fa4220:  7f 23 03 d5   pacibsp
0x223fa4224:  ff 43 04 d1   sub	sp, sp, #0x110
0x223fa4228:  f8 5f 0d a9   stp	x24, x23, [sp, #0xd0]
0x223fa422c:  f6 57 0e a9   stp	x22, x21, [sp, #0xe0]
0x223fa4230:  f4 4f 0f a9   stp	x20, x19, [sp, #0xf0]
0x223fa4234:  fd 7b 10 a9   stp	fp, lr, [sp, #0x100]
0x223fa4238:  fd 03 04 91   add	fp, sp, #0x100
0x223fa423c:  f3 03 00 aa   mov	x19, x0
0x223fa4240:  88 0d 26 90   adrp	x8, 0x270154000
0x223fa4244:  08 3d 46 f9   ldr	x8, [x8, #0xc78]
0x223fa4248:  08 01 40 f9   ldr	x8, [x8]
0x223fa424c:  a8 83 1c f8   stur	x8, [fp, #-0x38]
0x223fa4250:  e0 03 02 aa   mov	x0, x2
0x223fa4254:  17 49 03 95   bl	0x2280766b0
0x223fa4258:  f5 03 00 aa   mov	x21, x0
0x223fa425c:  e0 03 13 aa   mov	x0, x19
0x223fa4260:  14 49 03 95   bl	0x2280766b0
0x223fa4264:  f4 03 00 aa   mov	x20, x0
0x223fa4268:  2e 5c 03 95   bl	0x22807b320
0x223fa426c:  00 e4 00 6f   movi	v0.2d, #0
0x223fa4270:  e0 03 00 ad   stp	q0, q0, [sp]
0x223fa4274:  e0 03 01 ad   stp	q0, q0, [sp, #0x20]
0x223fa4278:  e0 03 15 aa   mov	x0, x21
0x223fa427c:  0d 49 03 95   bl	0x2280766b0
0x223fa4280:  f3 03 00 aa   mov	x19, x0
0x223fa4284:  e2 03 00 91   mov	x2, sp
0x223fa4288:  e3 23 01 91   add	x3, sp, #0x48
0x223fa428c:  04 02 80 52   mov	w4, #0x10
0x223fa4290:  18 76 01 95   bl	0x228001af0
0x223fa4294:  f5 03 00 aa   mov	x21, x0
0x223fa4298:  20 03 00 b4   cbz	x0, 0x223fa42fc
0x223fa429c:  e8 0b 40 f9   ldr	x8, [sp, #0x10]
0x223fa42a0:  16 01 40 f9   ldr	x22, [x8]
0x223fa42a4:  17 00 80 d2   mov	x23, #0
0x223fa42a8:  e8 0b 40 f9   ldr	x8, [sp, #0x10]
0x223fa42ac:  08 01 40 f9   ldr	x8, [x8]
0x223fa42b0:  1f 01 16 eb   cmp	x8, x22
0x223fa42b4:  60 00 00 54   b.eq	0x223fa42c0
0x223fa42b8:  e0 03 13 aa   mov	x0, x19
0x223fa42bc:  d9 48 03 95   bl	0x228076620
0x223fa42c0:  e8 07 40 f9   ldr	x8, [sp, #0x8]
0x223fa42c4:  00 79 77 f8   ldr	x0, [x8, x23, lsl #0x3]
0x223fa42c8:  3a 7d 01 95   bl	0x2280037b0
0x223fa42cc:  1f 58 00 f1   cmp	x0, #0x16
0x223fa42d0:  a1 04 00 54   b.ne	0x223fa4364
0x223fa42d4:  f7 06 00 91   add	x23, x23, #0x1
0x223fa42d8:  b5 06 00 f1   subs	x21, x21, #0x1
0x223fa42dc:  61 fe ff 54   b.ne	0x223fa42a8
0x223fa42e0:  e2 03 00 91   mov	x2, sp
0x223fa42e4:  e3 23 01 91   add	x3, sp, #0x48
0x223fa42e8:  e0 03 13 aa   mov	x0, x19
0x223fa42ec:  04 02 80 52   mov	w4, #0x10
0x223fa42f0:  00 76 01 95   bl	0x228001af0
0x223fa42f4:  f5 03 00 aa   mov	x21, x0
0x223fa42f8:  60 fd ff b5   cbnz	x0, 0x223fa42a4
0x223fa42fc:  e0 03 13 aa   mov	x0, x19
0x223fa4300:  e8 48 03 95   bl	0x2280766a0
0x223fa4304:  e0 03 13 aa   mov	x0, x19
0x223fa4308:  f2 75 01 95   bl	0x228001ad0
0x223fa430c:  88 06 40 f9   ldr	x8, [x20, #0x8]
0x223fa4310:  80 06 00 f9   str	x0, [x20, #0x8]
0x223fa4314:  e0 03 08 aa   mov	x0, x8
0x223fa4318:  e2 48 03 95   bl	0x2280766a0
0x223fa431c:  e0 03 14 aa   mov	x0, x20
0x223fa4320:  04 5c 03 95   bl	0x22807b330
0x223fa4324:  e0 03 14 aa   mov	x0, x20
0x223fa4328:  de 48 03 95   bl	0x2280766a0
0x223fa432c:  e0 03 13 aa   mov	x0, x19
0x223fa4330:  dc 48 03 95   bl	0x2280766a0
0x223fa4334:  a8 83 5c f8   ldur	x8, [fp, #-0x38]
0x223fa4338:  89 0d 26 90   adrp	x9, 0x270154000
0x223fa433c:  29 3d 46 f9   ldr	x9, [x9, #0xc78]
0x223fa4340:  29 01 40 f9   ldr	x9, [x9]
0x223fa4344:  3f 01 08 eb   cmp	x9, x8
0x223fa4348:  c1 01 00 54   b.ne	0x223fa4380
0x223fa434c:  fd 7b 50 a9   ldp	fp, lr, [sp, #0x100]
0x223fa4350:  f4 4f 4f a9   ldp	x20, x19, [sp, #0xf0]
0x223fa4354:  f6 57 4e a9   ldp	x22, x21, [sp, #0xe0]
0x223fa4358:  f8 5f 4d a9   ldp	x24, x23, [sp, #0xd0]
0x223fa435c:  ff 43 04 91   add	sp, sp, #0x110
0x223fa4360:  ff 0f 5f d6   retab
0x223fa4364:  a8 95 e9 90   adrp	x8, 0x1f7258000
0x223fa4368:  01 55 2d 91   add	x1, x8, #0xb55
0x223fa436c:  82 78 2b b0   adrp	x2, 0x27aeb5000
0x223fa4370:  42 20 14 91   add	x2, x2, #0x508
0x223fa4374:  e0 03 14 aa   mov	x0, x20
0x223fa4378:  d8 88 03 94   bl	0x2240866d8
0x223fa437c:  20 00 20 d4   brk	#0x1
0x223fa4380:  34 48 03 95   bl	0x228076450
0x223fa4384:  f5 03 00 aa   mov	x21, x0
