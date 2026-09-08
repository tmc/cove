#define memset shim_memset
#define memcpy shim_memcpy
#include "shim.c"
#undef memset
#undef memcpy
#include <assert.h>
#include <stdio.h>

static unsigned mirror_calls;
static EFI_STATUS mirror(GOP *g, void *b, u32 op, uintn sx, uintn sy,
    uintn dx, uintn dy, uintn w, uintn h, uintn delta) {
    (void)g; (void)b; (void)sx; (void)sy; (void)dx; (void)dy;
    (void)w; (void)h; (void)delta;
    assert(op != 1);
    mirror_calls++;
    return EFI_UNSUPPORTED;
}
int main(void) {
    u32 pixels[16], readback[24], color=0x123456;
    GOP apple={0};
    FB=pixels; FBW=4; FBH=4; FBPPSL=4;
    g_mode.FrameBufferSize=sizeof pixels;
    apple.Blt=mirror; g_apple=&apple;
    assert(my_setmode(&g_gop,0)==EFI_SUCCESS);
    for(int i=0;i<16;i++) assert(pixels[i]==0);
    assert(my_blt(&g_gop,&color,0,0,0,1,1,2,2,0)==EFI_SUCCESS);
    assert(pixels[5]==color && pixels[6]==color && pixels[9]==color && pixels[10]==color);
    for(int i=0;i<24;i++) readback[i]=0xfeed;
    assert(my_blt(&g_gop,readback,1,1,1,1,1,2,2,24)==EFI_SUCCESS);
    assert(readback[7]==color && readback[8]==color && readback[13]==color && readback[14]==color);
    assert(readback[6]==0xfeed && readback[9]==0xfeed);
    assert(my_blt(&g_gop,readback,2,1,1,0,0,2,2,24)==EFI_SUCCESS);
    assert(pixels[0]==color && pixels[1]==color && pixels[4]==color);
    for(int i=0;i<16;i++) pixels[i]=i;
    assert(my_blt(&g_gop,0,3,0,0,1,1,3,3,0)==EFI_SUCCESS);
    assert(pixels[5]==0 && pixels[6]==1 && pixels[7]==2);
    assert(pixels[9]==4 && pixels[13]==8 && pixels[15]==10);
    for(int i=0;i<16;i++) pixels[i]=i;
    assert(my_blt(&g_gop,0,3,1,1,0,0,3,3,0)==EFI_SUCCESS);
    assert(pixels[0]==5 && pixels[1]==6 && pixels[10]==15);
    assert(my_blt(&g_gop,&color,0,0,0,~(uintn)0,0,2,1,0)==EFI_INVALID_PARAMETER);
    assert(my_blt(&g_gop,0,7,0,0,0,0,1,1,0)==EFI_INVALID_PARAMETER);
    assert(my_query(&g_gop,0,0,0)==EFI_INVALID_PARAMETER);
    assert(my_setmode(&g_gop,1)==EFI_UNSUPPORTED);
    assert(mirror_calls>0);
    puts("Blt fill, readback, stride, overlap, bounds and mode checks passed");
}
