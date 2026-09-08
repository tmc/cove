#ifndef BREAK_RVA
#define BREAK_RVA 0
#endif
// shim.c — guest-side UEFI GOP shim + chainload for the Windows-on-VZ question.
//
// Experiment #4 (the Apple-decisive causal test). Boots from
// \EFI\BOOT\BOOTAA64.EFI on the ESP under Apple's VZEFIBootLoader, then
// chainloads \EFI\Microsoft\Boot\bootmgfw.efi from the SAME device.
//
// Two artifacts are built from this one source (see build.sh):
//   SHIM.EFI      (default)  : installs a RAM-backed *linear* GOP
//                              (PixelBlueGreenRedReserved8BitPerColor) over
//                              every existing GOP handle, THEN chainloads.
//   CHAINLOAD.EFI (-DNO_SHIM): identical entry + chainload, GOP left UNTOUCHED.
//
// The only variable between them is the GOP replacement. Baseline (unmodified
// WinPE on Apple) parks at ~6.4MB of disk reads. If CHAINLOAD also parks there
// but SHIM advances past it, the GOP intervention is associated with progress.
// A different total alone does not demonstrate continuing loader activity. Conclusions are CONDITIONAL: advancing disk reads prove
// progress only, NOT sole-blocker and NOT shippability. Direct linear-fb writes
// bypass Blt, so no post-ExitBootServices screen update is expected; its absence
// is not a failure.
//
// Freestanding; no libc, no EDK2. Minimal UEFI definitions inline.
// Build: see build.sh (Apple clang -> COFF, lld-link -> PE32+ EFI app).

typedef unsigned char       u8;
typedef unsigned short      u16;
typedef unsigned int        u32;
typedef unsigned long long  u64;
typedef unsigned long long  uintn;   // UINTN is 64-bit on aarch64
typedef unsigned short      c16;      // CHAR16 (-fshort-wchar makes L"" 16-bit)
typedef int                 bln;

typedef u64 EFI_STATUS;
typedef void *EFI_HANDLE;
#define EFIERR(x)      (0x8000000000000000ULL | (x))
#define EFI_SUCCESS            0ULL
#define EFI_INVALID_PARAMETER  EFIERR(2)
#define EFI_UNSUPPORTED        EFIERR(3)
#define EFI_OUT_OF_RESOURCES   EFIERR(9)
#define EFI_ABORTED            EFIERR(21)
#define EFI_ERROR(s)   (((s) >> 63) != 0)

#define EFI_FILE_MODE_READ   0x0000000000000001ULL
#define EFI_FILE_MODE_WRITE  0x0000000000000002ULL
#define EFI_FILE_MODE_CREATE 0x8000000000000000ULL
#define BY_PROTOCOL 2

// AllocatePages: AllocateAnyPages=0; memory type EfiReservedMemoryType=0 so the
// OS cannot reclaim the framebuffer region.
#define ALLOCATE_ANY_PAGES     0
#define EFI_RESERVED_MEM_TYPE  0
#define EFI_BOOT_SERVICES_DATA 4

typedef struct { u32 d1; u16 d2; u16 d3; u8 d4[8]; } EFI_GUID;

typedef struct { u64 Signature; u32 Revision; u32 HeaderSize; u32 CRC32; u32 Reserved; } EFI_TABLE_HEADER;

struct _SIMPLE_TEXT_OUTPUT;
typedef EFI_STATUS (*EFI_TEXT_STRING)(struct _SIMPLE_TEXT_OUTPUT *This, c16 *String);
typedef struct _SIMPLE_TEXT_OUTPUT { void *Reset; EFI_TEXT_STRING OutputString; } SIMPLE_TEXT_OUTPUT;

typedef struct {
    u32 Version, HorizontalResolution, VerticalResolution, PixelFormat;
    u32 RedMask, GreenMask, BlueMask, ReservedMask, PixelsPerScanLine;
} GOP_MODE_INFO;

typedef struct {
    u32 MaxMode, Mode;
    GOP_MODE_INFO *Info;
    uintn SizeOfInfo;
    u64 FrameBufferBase;
    uintn FrameBufferSize;
} GOP_MODE;

struct _GOP;
typedef EFI_STATUS (*GOP_QUERY_MODE)(struct _GOP *This, u32 ModeNumber, uintn *SizeOfInfo, GOP_MODE_INFO **Info);
typedef EFI_STATUS (*GOP_SET_MODE)(struct _GOP *This, u32 ModeNumber);
typedef EFI_STATUS (*GOP_BLT)(struct _GOP *This, void *BltBuffer, u32 BltOperation,
    uintn SrcX, uintn SrcY, uintn DstX, uintn DstY, uintn Width, uintn Height, uintn Delta);
typedef struct _GOP { GOP_QUERY_MODE QueryMode; GOP_SET_MODE SetMode; GOP_BLT Blt; GOP_MODE *Mode; } GOP;

typedef struct {
    u32 Revision; EFI_HANDLE ParentHandle; void *SystemTable; EFI_HANDLE DeviceHandle;
    void *FilePath; void *Reserved; u32 LoadOptionsSize; void *LoadOptions; void *ImageBase; u64 ImageSize;
} LOADED_IMAGE;

// EFI_DEVICE_PATH header; Length is a 2-byte little-endian field.
typedef struct { u8 Type; u8 SubType; u8 Length[2]; } EFI_DEVICE_PATH;

struct _EFI_FILE;
typedef EFI_STATUS (*EFI_FILE_OPEN)(struct _EFI_FILE *This, struct _EFI_FILE **New, c16 *Name, u64 Mode, u64 Attr);
typedef EFI_STATUS (*EFI_FILE_CLOSE)(struct _EFI_FILE *This);
typedef EFI_STATUS (*EFI_FILE_DELETE)(struct _EFI_FILE *This);
typedef EFI_STATUS (*EFI_FILE_IO)(struct _EFI_FILE *This, uintn *BufferSize, void *Buffer);
typedef EFI_STATUS (*EFI_FILE_SETPOS)(struct _EFI_FILE *This, u64 Position);
typedef EFI_STATUS (*EFI_FILE_FLUSH)(struct _EFI_FILE *This);
typedef struct _EFI_FILE {
    u64 Revision;
    EFI_FILE_OPEN Open; EFI_FILE_CLOSE Close; EFI_FILE_DELETE Delete;
    EFI_FILE_IO Read; EFI_FILE_IO Write;
    void *GetPosition; EFI_FILE_SETPOS SetPosition; void *GetInfo; void *SetInfo;
    EFI_FILE_FLUSH Flush;
} EFI_FILE;

struct _SFS;
typedef EFI_STATUS (*SFS_OPEN_VOLUME)(struct _SFS *This, EFI_FILE **Root);
typedef struct _SFS { u64 Revision; SFS_OPEN_VOLUME OpenVolume; } SFS;

typedef struct {
    EFI_TABLE_HEADER Hdr;
    void *RaiseTPL, *RestoreTPL;
    EFI_STATUS (*AllocatePages)(u32 Type, u32 MemType, uintn Pages, u64 *Memory);
    void *FreePages, *GetMemoryMap;
    EFI_STATUS (*AllocatePool)(u32 Type, uintn Size, void **Buffer);
    EFI_STATUS (*FreePool)(void *Buffer);
    void *CreateEvent, *SetTimer, *WaitForEvent, *SignalEvent, *CloseEvent, *CheckEvent;
    void *InstallProtocolInterface;
    EFI_STATUS (*ReinstallProtocolInterface)(EFI_HANDLE Handle, EFI_GUID *Protocol, void *Old, void *New);
    void *UninstallProtocolInterface;
    EFI_STATUS (*HandleProtocol)(EFI_HANDLE Handle, EFI_GUID *Protocol, void **Interface);
    void *Reserved, *RegisterProtocolNotify, *LocateHandle, *LocateDevicePath, *InstallConfigurationTable;
    EFI_STATUS (*LoadImage)(bln BootPolicy, EFI_HANDLE Parent, void *DevicePath, void *Src, uintn SrcSize, EFI_HANDLE *Image);
    EFI_STATUS (*StartImage)(EFI_HANDLE Image, uintn *ExitDataSize, c16 **ExitData);
    void *Exit, *UnloadImage, *ExitBootServices, *GetNextMonotonicCount;
    EFI_STATUS (*Stall)(uintn Microseconds);
    void *SetWatchdogTimer, *ConnectController, *DisconnectController, *OpenProtocol, *CloseProtocol;
    void *OpenProtocolInformation, *ProtocolsPerHandle;
    EFI_STATUS (*LocateHandleBuffer)(u32 SearchType, EFI_GUID *Protocol, void *Key, uintn *NoHandles, EFI_HANDLE **Buffer);
    EFI_STATUS (*LocateProtocol)(EFI_GUID *Protocol, void *Registration, void **Interface);
} BOOT_SERVICES;

typedef struct {
    EFI_TABLE_HEADER Hdr;
    c16 *FirmwareVendor; u32 FirmwareRevision;
    EFI_HANDLE ConsoleInHandle; void *ConIn;
    EFI_HANDLE ConsoleOutHandle; SIMPLE_TEXT_OUTPUT *ConOut;
    EFI_HANDLE StdErrHandle; void *StdErr;
    void *RuntimeServices; BOOT_SERVICES *BootServices;
} SYSTEM_TABLE;

#ifndef NO_SHIM
static EFI_GUID GOP_GUID          = {0x9042a9de,0x23dc,0x4a38,{0x96,0xfb,0x7a,0xde,0xd0,0x80,0x51,0x6a}};
#endif
static EFI_GUID SFS_GUID          = {0x964e5b22,0x6459,0x11d2,{0x8e,0x39,0x00,0xa0,0xc9,0x69,0x72,0x3b}};
static EFI_GUID LOADED_IMAGE_GUID = {0x5b1b31a1,0x9562,0x11d2,{0x8e,0x3f,0x00,0xa0,0xc9,0x69,0x72,0x3b}};
static EFI_GUID DEVPATH_GUID      = {0x09576e91,0x6d3f,0x11d2,{0x8e,0x39,0x00,0xa0,0xc9,0x69,0x72,0x3b}};

void *memset(void *d, int c, uintn n){ u8 *p=d; while(n--) *p++=(u8)c; return d; }
void *memcpy(void *d, const void *s, uintn n){ u8 *a=d; const u8 *b=s; while(n--) *a++=*b++; return d; }

static SYSTEM_TABLE *ST;
static BOOT_SERVICES *BS;

static char REPORT[32*1024];
static uintn RLEN = 0;
static EFI_FILE *OUT = 0;
static uintn WRITTEN = 0;
static int PERSIST = 0;   // 0 none/unknown, 1 ok, 2 failed

static void putc16_console(char c){
    if (!ST || !ST->ConOut || !ST->ConOut->OutputString) return;
    if (c=='\n'){ c16 cr[3]={'\r','\n',0}; ST->ConOut->OutputString(ST->ConOut, cr); return; }
    c16 s[2]; s[0]=(c16)(u8)c; s[1]=0;
    ST->ConOut->OutputString(ST->ConOut, s);
}

static void emit(const char *s){
    while (*s){
        if (RLEN < sizeof(REPORT)-1) REPORT[RLEN++] = *s;
        putc16_console(*s);
        s++;
    }
}

static void emit_u64(u64 v, int hex){
    char tmp[32]; int n=0;
    if (hex){
        emit("0x");
        for (int i=60;i>=0;i-=4){ int d=(int)((v>>i)&0xF);
            if (n || d || i==0){ char c=d<10?('0'+d):('a'+d-10); char b[2]={c,0}; emit(b); n=1; } }
        if(!n) emit("0");
    } else {
        if (v==0){ emit("0"); return; }
        int i=0; while(v){ tmp[i++]='0'+(int)(v%10); v/=10; }
        while(i--){ char b[2]={tmp[i],0}; emit(b); }
    }
}

static void flush_report(void){
    if (!OUT){ return; }
    if (RLEN <= WRITTEN) return;
    uintn want = RLEN - WRITTEN;
    uintn n = want;
    EFI_STATUS ws = OUT->Write(OUT, &n, REPORT + WRITTEN);
    if (EFI_ERROR(ws) || n != want){ PERSIST = 2; return; }
    WRITTEN += n;
    EFI_STATUS fs = OUT->Flush(OUT);
    if (EFI_ERROR(fs)){ PERSIST = 2; return; }
    if (PERSIST != 2) PERSIST = 1;
}

// ---- synthetic linear GOP (SHIM.EFI only) --------------------------------
#ifndef NO_SHIM
static GOP_MODE_INFO g_info;
static GOP_MODE      g_mode;
static GOP           g_gop;
static GOP          *g_apple = 0;   // real firmware GOP, kept for the Blt mirror
static u32          *FB   = 0;      // our reserved linear framebuffer (32bpp)
static u32           FBW  = 0, FBH = 0, FBPPSL = 0;  // width, height, pixels/scanline

// EFI_GRAPHICS_OUTPUT_BLT_PIXEL is {Blue,Green,Red,Reserved} = 4 bytes; our
// framebuffer is PixelBlueGreenRedReserved8BitPerColor, an identical layout, so
// pixels copy as plain 32-bit words with no channel swizzle.

static EFI_STATUS my_query(GOP *This, u32 mode, uintn *sz, GOP_MODE_INFO **info){
    (void)This;
    if (!sz || !info) return EFI_INVALID_PARAMETER;   // validate output pointers
    if (mode != 0) return EFI_INVALID_PARAMETER;      // only mode 0 exists
    // Return a pool copy: callers (bootmgfw) FreePool the result.
    GOP_MODE_INFO *cp = 0;
    if (EFI_ERROR(BS->AllocatePool(EFI_BOOT_SERVICES_DATA, sizeof(GOP_MODE_INFO), (void**)&cp)) || !cp)
        return EFI_OUT_OF_RESOURCES;
    memcpy(cp, &g_info, sizeof(g_info));
    *sz = sizeof(GOP_MODE_INFO); *info = cp;
    return EFI_SUCCESS;
}

static EFI_STATUS my_setmode(GOP *This, u32 mode){
    (void)This;
    if (mode != 0) return EFI_UNSUPPORTED;
    // Per spec, setting a mode clears the framebuffer to black.
    if (FB) memset(FB, 0, g_mode.FrameBufferSize);
    return EFI_SUCCESS;
}

// Overflow-safe: does video rect (x,y) size (w,h) fit the WxH mode? Checked by
// subtraction (x+w can wrap for adversarial uintn inputs). Assumes w,h >= 1.
static int in_video(uintn x, uintn y, uintn w, uintn h){
    return !(x > FBW || w > FBW - x || y > FBH || h > FBH - y);
}
// Overflow-safe: can a BltBuffer row of `bpr` pixels hold columns [bx, bx+w)?
static int bb_row_ok(uintn bpr, uintn bx, uintn w){
    return !(bx > bpr || w > bpr - bx);
}

// Full four-operation Blt against our reserved linear framebuffer, honoring the
// Delta byte-stride and bounds. Video coordinates are checked against the mode;
// the BltBuffer is trusted per the given coords/Delta (the caller owns its size).
// Video-writing ops (Fill/BufferToVideo/VideoToVideo) mirror best-effort to the
// real firmware GOP so output is visible pre-ExitBootServices; a mirror error
// never fails the caller. VideoToBltBuffer (a READ) is NEVER mirrored: forwarding
// it would make Apple overwrite the caller's buffer with its own stale pixels,
// clobbering the authoritative synthetic read.
static EFI_STATUS my_blt(GOP *This, void *buf, u32 op, uintn sx, uintn sy,
                         uintn dx, uintn dy, uintn w, uintn h, uintn delta){
    (void)This;
    if (w == 0 || h == 0) return EFI_SUCCESS;         // nothing to do
    if (!FB) return EFI_INVALID_PARAMETER;
    u32 *bb  = (u32*)buf;
    // Delta is a BYTE stride; for a 32bpp buffer it must be pixel-aligned. Reject
    // a misaligned stride rather than silently truncate delta/4.
    if (delta && (delta & 3)) return EFI_INVALID_PARAMETER;
    uintn bpr = delta ? (delta / 4) : w;              // BltBuffer pixels per row

    switch (op) {
    case 0: { // EfiBltVideoFill: BltBuffer[0] fills video rect (dx,dy,w,h)
        if (!bb) return EFI_INVALID_PARAMETER;
        if (!in_video(dx, dy, w, h)) return EFI_INVALID_PARAMETER;
        u32 px = bb[0];
        for (uintn y = 0; y < h; y++){ u32 *r = FB + (dy+y)*FBPPSL + dx; for (uintn x=0;x<w;x++) r[x]=px; }
        break;
    }
    case 1: { // EfiBltVideoToBltBuffer: video(sx,sy) -> BltBuffer(dx,dy)
        if (!bb) return EFI_INVALID_PARAMETER;
        if (!in_video(sx, sy, w, h)) return EFI_INVALID_PARAMETER;
        if (!bb_row_ok(bpr, dx, w)) return EFI_INVALID_PARAMETER;   // BltBuffer row holds [dx,dx+w)
        for (uintn y = 0; y < h; y++){ u32 *s = FB + (sy+y)*FBPPSL + sx; u32 *d = bb + (dy+y)*bpr + dx; for (uintn x=0;x<w;x++) d[x]=s[x]; }
        break;
    }
    case 2: { // EfiBltBufferToVideo: BltBuffer(sx,sy) -> video(dx,dy)
        if (!bb) return EFI_INVALID_PARAMETER;
        if (!in_video(dx, dy, w, h)) return EFI_INVALID_PARAMETER;
        if (!bb_row_ok(bpr, sx, w)) return EFI_INVALID_PARAMETER;   // BltBuffer row holds [sx,sx+w)
        for (uintn y = 0; y < h; y++){ u32 *s = bb + (sy+y)*bpr + sx; u32 *d = FB + (dy+y)*FBPPSL + dx; for (uintn x=0;x<w;x++) d[x]=s[x]; }
        break;
    }
    case 3: { // EfiBltVideoToVideo: video(sx,sy) -> video(dx,dy), overlap-safe
        if (!in_video(sx, sy, w, h) || !in_video(dx, dy, w, h)) return EFI_INVALID_PARAMETER;
        int backward = (dy > sy) || (dy == sy && dx > sx);  // copy dir by memory order
        if (backward){
            for (uintn y = h; y-- > 0; ){ u32 *s = FB + (sy+y)*FBPPSL + sx; u32 *d = FB + (dy+y)*FBPPSL + dx; for (uintn x=w; x-- > 0; ) d[x]=s[x]; }
        } else {
            for (uintn y = 0; y < h; y++){ u32 *s = FB + (sy+y)*FBPPSL + sx; u32 *d = FB + (dy+y)*FBPPSL + dx; for (uintn x=0;x<w;x++) d[x]=s[x]; }
        }
        break;
    }
    default:
        return EFI_INVALID_PARAMETER;
    }

    // Mirror only video-writing ops; never op 1 (a read into the caller's buffer).
    if (op != 1 && g_apple && g_apple->Blt) g_apple->Blt(g_apple, buf, op, sx, sy, dx, dy, w, h, delta);
    return EFI_SUCCESS;
}
#endif

// device-path length up to but excluding the END node
static uintn dp_len_no_end(EFI_DEVICE_PATH *dp){
    u8 *p = (u8*)dp; uintn total = 0;
    for(;;){
        EFI_DEVICE_PATH *n = (EFI_DEVICE_PATH*)p;
        uintn l = (uintn)n->Length[0] | ((uintn)n->Length[1] << 8);
        if (l < 4) break;                                   // malformed
        if (n->Type == 0x7F && n->SubType == 0xFF) break;   // END of entire path
        total += l; p += l;
    }
    return total;
}

// \EFI\Microsoft\Boot\bootmgfw.efi as CHAR16 (explicit, avoids wchar_t typing)
static c16 BOOTMGFW_PATH[] = {
    '\\','E','F','I','\\','M','i','c','r','o','s','o','f','t','\\',
    'B','o','o','t','\\','b','o','o','t','m','g','f','w','.','e','f','i', 0
};


volatile u64 entry_trap[24], entry_saved[8];
volatile u64 emu_count, emu_reason, emu_pcs[64], emu_gp[32], emu_ranges[64], emu_nrange;
extern u64 entry_call(void *start, EFI_HANDLE child);
static u32 le32(const u8 *p) { return (u32)p[0] | (u32)p[1]<<8 | (u32)p[2]<<16 | (u32)p[3]<<24; }
static EFI_STATUS trace_entry(EFI_HANDLE child) {
    u64 el, sel;
    __asm__ volatile("mrs %0, CurrentEL\n mrs %1, SPSel" : "=r"(el), "=r"(sel));
    if (el != 4 || sel != 1) return EFI_UNSUPPORTED;
    LOADED_IMAGE *loaded=0;
    EFI_STATUS st=BS->HandleProtocol(child, &LOADED_IMAGE_GUID, (void**)&loaded);
    if (EFI_ERROR(st) || !loaded || !loaded->ImageBase || loaded->ImageSize < 0x100) return EFI_UNSUPPORTED;
    u8 *base=loaded->ImageBase; u64 size=loaded->ImageSize;
    if (base[0]!='M' || base[1]!='Z') return EFI_UNSUPPORTED;
    u64 pe=le32(base+0x3c);
    if (pe > size-0x80 || le32(base+pe)!=0x4550 || base[pe+4]!=0x64 || base[pe+5]!=0xaa || base[pe+24]!=0x0b || base[pe+25]!=2) return EFI_UNSUPPORTED;
    u64 image_entry=le32(base+pe+40);
    u64 rva=BREAK_RVA ? BREAK_RVA : image_entry;
    u64 nsec=(u32)base[pe+6] | (u32)base[pe+7]<<8;
    u64 opt=(u32)base[pe+20] | (u32)base[pe+21]<<8;
    u64 sections=pe+24+opt;
    if (opt<112 || sections>size || nsec>(size-sections)/40) return EFI_UNSUPPORTED;
    int executable=0;
    for (u64 i=0;i<nsec;i++) {
        const u8 *sec=base+sections+i*40;
        u64 va=le32(sec+12), vs=le32(sec+8);
        if ((le32(sec+36)&0x20000000) && vs>=4 && va<=size && vs<=size-va) {
            if (emu_nrange>=32) return EFI_UNSUPPORTED;
            emu_ranges[emu_nrange*2]=(u64)base+va;
            emu_ranges[emu_nrange*2+1]=(u64)base+va+vs-4;
            emu_nrange++;
            if (rva>=va && rva-va<=vs-4) executable=1;
        }
    }
    if (!executable) return EFI_UNSUPPORTED;
    if (rva > size-4 || (rva&3)) return EFI_UNSUPPORTED;
    u32 *entry=(u32*)(base+rva), original=*entry;
    emit("loaded image base "); emit_u64((u64)base,1); emit(" size "); emit_u64(size,1);
    emit("\nimage entry RVA "); emit_u64(image_entry,1);
    emit("\nbreakpoint RVA "); emit_u64(rva,1); emit(" expected ELR "); emit_u64((u64)entry,1);
    emit("\noriginal instruction "); emit_u64(original,1); emit("\n"); flush_report();
    entry_saved[7]=(u64)entry;
    /* Fault-only: loaded image bytes remain unchanged. */
    u64 tpl=((u64(*)(u64))BS->RaiseTPL)(31); ((void(*)(u64))BS->RestoreTPL)(tpl);
    u64 result=entry_call((void*)BS->StartImage,child);

    ((void(*)(u64))BS->RestoreTPL)(tpl);
    emit("entry_call returned "); emit_u64(result,1); emit("\nobserved ELR "); emit_u64(entry_trap[0],1);
    emit("\nESR "); emit_u64(entry_trap[1],1); emit("\nFAR "); emit_u64(entry_trap[2],1);
    emit("\nSPSR "); emit_u64(entry_trap[3],1); emit("\nchild SP "); emit_u64(entry_trap[4],1);
    emit("\nchild LR "); emit_u64(entry_trap[5],1); emit("\n");
    emit("emulated reads "); emit_u64(emu_count,0); emit(" stop reason "); emit_u64(emu_reason,0); emit(" (1=64-read-limit 2=different-synchronous-exception 3=outside-executable-image)\n");
    for (u64 i=0;i<emu_count && i<64;i++) { emit("PMCCNTR pc["); emit_u64(i,0); emit("] "); emit_u64(emu_pcs[i],1); emit("\n"); }
    for (u64 i=0;i<32;i++) { emit("saved x"); emit_u64(i,0); emit(" "); emit_u64(emu_gp[i],1); emit("\n"); }
    emit("counter values are generic timer ticks, not CPU-cycle-equivalent\n");
    if (result==0 && !entry_trap[0]) emit("CHILD RETURNED WITHOUT MATCHING FAULT\n");
    emit("one-shot; discard guest after report\n"); flush_report();
    return EFI_ABORTED;
}

static EFI_STATUS chainload(EFI_HANDLE image, LOADED_IMAGE *li){
    EFI_DEVICE_PATH *base = 0;
    if (EFI_ERROR(BS->HandleProtocol(li->DeviceHandle, &DEVPATH_GUID, (void**)&base)) || !base){
        emit("chainload: no device path on boot volume\n");
        return EFI_UNSUPPORTED;
    }
    uintn baselen = dp_len_no_end(base);

    uintn plen = 0; while (BOOTMGFW_PATH[plen]) plen++;   // CHAR16 count, excl NUL
    uintn nodelen = 4 + (plen + 1) * 2;                    // hdr + string incl NUL
    uintn total = baselen + nodelen + 4;                   // + END node

    // Reserved so it survives through the child's execution; never freed.
    u64 buf64 = 0;
    uintn pages = (total + 4095) / 4096;
    if (EFI_ERROR(BS->AllocatePages(ALLOCATE_ANY_PAGES, EFI_RESERVED_MEM_TYPE, pages, &buf64)) || !buf64){
        emit("chainload: device-path alloc failed\n");
        return EFI_OUT_OF_RESOURCES;
    }
    u8 *dp = (u8*)buf64;
    memcpy(dp, base, baselen);                             // volume path (no END)
    EFI_DEVICE_PATH *fn = (EFI_DEVICE_PATH*)(dp + baselen);
    fn->Type = 0x04;                                       // MEDIA_DEVICE_PATH
    fn->SubType = 0x04;                                    // MEDIA_FILEPATH_DP
    fn->Length[0] = (u8)(nodelen & 0xff);
    fn->Length[1] = (u8)(nodelen >> 8);
    memcpy(dp + baselen + 4, BOOTMGFW_PATH, (plen + 1) * 2);
    EFI_DEVICE_PATH *end = (EFI_DEVICE_PATH*)(dp + baselen + nodelen);
    end->Type = 0x7F; end->SubType = 0xFF; end->Length[0] = 4; end->Length[1] = 0;

    EFI_HANDLE child = 0;
    EFI_STATUS ls = BS->LoadImage(0 /*BootPolicy FALSE*/, image, dp, 0, 0, &child);
    emit("LoadImage(bootmgfw) status "); emit_u64(ls, 1); emit("\n");
    if (EFI_ERROR(ls) || !child) return ls;

    emit("StartImage(bootmgfw)... (no return on success)\n");
    flush_report();                 // persist everything BEFORE handing over
    // deliberately do NOT close OUT: keep buffers/handles alive through child
    EFI_STATUS ss = trace_entry(child);
    emit("trace_entry returned "); emit_u64(ss, 1); emit(" (StartImage abandoned only on matching fault)\n");
    return ss;
}

EFI_STATUS efi_main(EFI_HANDLE image, SYSTEM_TABLE *st){
    ST = st; BS = st->BootServices;

    LOADED_IMAGE *li = 0; SFS *fs = 0; EFI_FILE *root = 0;
    if (!EFI_ERROR(BS->HandleProtocol(image, &LOADED_IMAGE_GUID, (void**)&li)) && li &&
        !EFI_ERROR(BS->HandleProtocol(li->DeviceHandle, &SFS_GUID, (void**)&fs)) && fs &&
        !EFI_ERROR(fs->OpenVolume(fs, &root)) && root){
#ifdef NO_SHIM
        c16 name[] = {'C','H','A','I','N','L','O','G','.','T','X','T',0};
#else
        c16 name[] = {'S','H','I','M','L','O','G','.','T','X','T',0};
#endif
        // Truncate defensively: delete any prior copy, then create fresh empty.
        EFI_FILE *pre = 0;
        if (!EFI_ERROR(root->Open(root, &pre, name,
                EFI_FILE_MODE_CREATE|EFI_FILE_MODE_READ|EFI_FILE_MODE_WRITE, 0)) && pre){
            if (pre->Delete) pre->Delete(pre); else pre->Close(pre);
        }
        if (EFI_ERROR(root->Open(root, &OUT, name,
                EFI_FILE_MODE_CREATE|EFI_FILE_MODE_READ|EFI_FILE_MODE_WRITE, 0))) OUT = 0;
        if (OUT && OUT->SetPosition) OUT->SetPosition(OUT, 0);
    }

#ifdef NO_SHIM
    emit("cove bounded guest PMCCNTR read emulation\n");
    emit("GOP: left UNTOUCHED (Apple firmware GOP as-is)\n");
#else
    emit("cove Windows-on-VZ GOP SHIM (experiment #4)\n");
#endif
    emit("============================================================\n");
    emit("Report file open: "); emit(OUT?"yes\n":"NO (console only)\n");
    emit("NOTE: no code patch or stepping; max64 PMCCNTR reads return CNTVCT ticks.\n");


    flush_report();

#ifndef NO_SHIM
    // Install the synthetic linear GOP over EVERY existing GOP handle so
    // bootmgfw cannot find the BltOnly one wherever it looks. The SHIM run is
    // valid only if the swap is COMPLETE; otherwise we ABORT without chainloading
    // rather than imply a successful swap (a park then would be uninterpretable).
    int swap_ok = 0;
    emit("\n== installing synthetic linear GOP ==\n");
    uintn nh = 0; EFI_HANDLE *handles = 0;
    EFI_STATUS hs = BS->LocateHandleBuffer(BY_PROTOCOL, &GOP_GUID, 0, &nh, &handles);
    if (EFI_ERROR(hs) || nh == 0 || !handles){
        emit("LocateHandleBuffer(GOP) failed, status "); emit_u64(hs,1); emit("\n");
    } else {
        // Take resolution from the first firmware GOP; keep it as mirror target.
        GOP *first = 0;
        if (!EFI_ERROR(BS->HandleProtocol(handles[0], &GOP_GUID, (void**)&first)) && first && first->Mode && first->Mode->Info){
            g_apple = first;
            g_info.HorizontalResolution = first->Mode->Info->HorizontalResolution;
            g_info.VerticalResolution   = first->Mode->Info->VerticalResolution;
        } else {
            g_info.HorizontalResolution = 1920; g_info.VerticalResolution = 1200;
        }
        u32 w = g_info.HorizontalResolution, h = g_info.VerticalResolution;
        g_info.Version = 0;
        g_info.PixelFormat = 1;                 // BGRReserved8 (linear, Windows-preferred)
        g_info.PixelsPerScanLine = w;
        g_mode.MaxMode = 1; g_mode.Mode = 0; g_mode.Info = &g_info; g_mode.SizeOfInfo = sizeof(g_info);
        g_mode.FrameBufferSize = (uintn)w * h * 4;
        g_gop.QueryMode = my_query; g_gop.SetMode = my_setmode; g_gop.Blt = my_blt; g_gop.Mode = &g_mode;

        // Reserved framebuffer pages: the OS must not reclaim this region.
        u64 fb = 0; uintn fbpages = (g_mode.FrameBufferSize + 4095) / 4096;
        if (EFI_ERROR(BS->AllocatePages(ALLOCATE_ANY_PAGES, EFI_RESERVED_MEM_TYPE, fbpages, &fb)) || !fb){
            emit("framebuffer AllocatePages failed\n");
        } else {
            memset((void*)fb, 0, g_mode.FrameBufferSize);
            g_mode.FrameBufferBase = fb;
            FB = (u32*)fb; FBW = w; FBH = h; FBPPSL = w;   // arm the Blt implementation
            emit("linear GOP: "); emit_u64(w,0); emit("x"); emit_u64(h,0);
            emit(" BGRReserved8 fb="); emit_u64(fb,1);
            emit(" size="); emit_u64((u64)g_mode.FrameBufferSize,0); emit("\n");
            emit("GOP handles found: "); emit_u64(nh,0); emit("\n");
            uintn done = 0;
            for (uintn i=0;i<nh;i++){
                GOP *old = 0;
                if (EFI_ERROR(BS->HandleProtocol(handles[i], &GOP_GUID, (void**)&old)) || !old) continue;
                EFI_STATUS rs = BS->ReinstallProtocolInterface(handles[i], &GOP_GUID, old, &g_gop);
                emit("  handle #"); emit_u64(i,0); emit(" reinstall status "); emit_u64(rs,1); emit("\n");
                if (!EFI_ERROR(rs)) done++;
            }
            emit("reinstalled over "); emit_u64(done,0); emit("/"); emit_u64(nh,0); emit(" handle(s)\n");
            if (done == nh && done > 0) swap_ok = 1;   // COMPLETE swap only
        }
        if (BS->FreePool) BS->FreePool(handles);
    }
    if (!swap_ok){
        emit("\nABORT: GOP replacement incomplete; NOT chainloading. A park here would\n");
        emit("       be uninterpretable, so no chainload and no implied swap.\n");
        flush_report();
        if (OUT) OUT->Close(OUT);
        emit("\nPERSIST: ");
        emit(PERSIST==1 ? "ok (report written+flushed)\n"
            : PERSIST==2 ? "FAILED (write/flush error)\n"
            : "none (no writable volume)\n");
        if (BS->Stall) BS->Stall(5ULL*1000*1000);
        return EFI_ABORTED;
    }
    flush_report();
#endif

    emit("\n== chainloading bootmgfw ==\n"); flush_report();
    EFI_STATUS cs = EFI_SUCCESS;
    if (li) cs = chainload(image, li);
    else { emit("no LoadedImage; cannot chainload\n"); cs = EFI_UNSUPPORTED; }

    // Only reached if chainload failed / bootmgfw exited.
    emit("\nchainload result "); emit_u64(cs,1); emit("\n");
    flush_report();
    if (OUT) OUT->Close(OUT);

    emit("\nPERSIST: ");
    emit(PERSIST==1 ? "ok (report written+flushed)\n"
        : PERSIST==2 ? "FAILED (write/flush error)\n"
        : "none (no writable volume)\n");

    if (BS->Stall) BS->Stall(5ULL*1000*1000);
    return cs;
}
