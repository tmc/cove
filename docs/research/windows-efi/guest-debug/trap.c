// diag.c — guest-side UEFI diagnostic for the Windows-on-VZ GOP question.
//
// Experiment #2: run this as \EFI\BOOT\BOOTAA64.EFI on a writable FAT volume
// under Apple's VZEFIBootLoader (via cove/winbootprobe). It measures what the
// firmware actually exposes to an OS loader like bootmgfw.efi:
//   - firmware vendor / UEFI revision
//   - EVERY GOP handle and EVERY mode: resolution, PixelFormat (the crux:
//     BltOnly vs a linear BGRR8/RGBR8 format), pixels-per-scanline, fb base/size
//   - ACPI: RSDP -> XSDT table signatures (what platform tables the loader sees)
//
// "report persistence first": EFIDIAG.TXT is written and FLUSHED section by
// section, and each Write/Flush status + byte count is checked; a final
// console-only line reports whether persistence actually succeeded.
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
#define EFI_ERROR(s)   (((s) >> 63) != 0)

#define EFI_FILE_MODE_READ   0x0000000000000001ULL
#define EFI_FILE_MODE_WRITE  0x0000000000000002ULL
#define EFI_FILE_MODE_CREATE 0x8000000000000000ULL
#define BY_PROTOCOL 2

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
typedef struct _GOP { GOP_QUERY_MODE QueryMode; void *SetMode; void *Blt; GOP_MODE *Mode; } GOP;

typedef struct {
    u32 Revision; EFI_HANDLE ParentHandle; void *SystemTable; EFI_HANDLE DeviceHandle;
} LOADED_IMAGE;

struct _EFI_FILE;
typedef EFI_STATUS (*EFI_FILE_OPEN)(struct _EFI_FILE *This, struct _EFI_FILE **New, c16 *Name, u64 Mode, u64 Attr);
typedef EFI_STATUS (*EFI_FILE_CLOSE)(struct _EFI_FILE *This);
typedef EFI_STATUS (*EFI_FILE_IO)(struct _EFI_FILE *This, uintn *BufferSize, void *Buffer);
typedef EFI_STATUS (*EFI_FILE_FLUSH)(struct _EFI_FILE *This);
typedef struct _EFI_FILE {
    u64 Revision;
    EFI_FILE_OPEN Open; EFI_FILE_CLOSE Close; void *Delete;
    EFI_FILE_IO Read; EFI_FILE_IO Write;
    void *GetPosition; void *SetPosition; void *GetInfo; void *SetInfo;
    EFI_FILE_FLUSH Flush;
} EFI_FILE;

struct _SFS;
typedef EFI_STATUS (*SFS_OPEN_VOLUME)(struct _SFS *This, EFI_FILE **Root);
typedef struct _SFS { u64 Revision; SFS_OPEN_VOLUME OpenVolume; } SFS;

typedef struct {
    EFI_TABLE_HEADER Hdr;
    void *RaiseTPL, *RestoreTPL, *AllocatePages, *FreePages, *GetMemoryMap;
    EFI_STATUS (*AllocatePool)(u32 Type, uintn Size, void **Buffer);
    EFI_STATUS (*FreePool)(void *Buffer);
    void *CreateEvent, *SetTimer, *WaitForEvent, *SignalEvent, *CloseEvent, *CheckEvent;
    void *InstallProtocolInterface, *ReinstallProtocolInterface, *UninstallProtocolInterface;
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

typedef struct { EFI_GUID VendorGuid; void *VendorTable; } EFI_CONFIG_TABLE;

typedef struct {
    EFI_TABLE_HEADER Hdr;
    c16 *FirmwareVendor; u32 FirmwareRevision;
    EFI_HANDLE ConsoleInHandle; void *ConIn;
    EFI_HANDLE ConsoleOutHandle; SIMPLE_TEXT_OUTPUT *ConOut;
    EFI_HANDLE StdErrHandle; void *StdErr;
    void *RuntimeServices; BOOT_SERVICES *BootServices;
    uintn NumberOfTableEntries; EFI_CONFIG_TABLE *ConfigurationTable;
} SYSTEM_TABLE;

static EFI_GUID GOP_GUID          = {0x9042a9de,0x23dc,0x4a38,{0x96,0xfb,0x7a,0xde,0xd0,0x80,0x51,0x6a}};
static EFI_GUID SFS_GUID          = {0x964e5b22,0x6459,0x11d2,{0x8e,0x39,0x00,0xa0,0xc9,0x69,0x72,0x3b}};
static EFI_GUID LOADED_IMAGE_GUID = {0x5b1b31a1,0x9562,0x11d2,{0x8e,0x3f,0x00,0xa0,0xc9,0x69,0x72,0x3b}};
static EFI_GUID ACPI20_GUID       = {0x8868e871,0xe4f1,0x11d3,{0xbc,0x22,0x00,0x80,0xc7,0x3c,0x88,0x81}};
static EFI_GUID ACPI10_GUID       = {0xeb9d2d30,0x2d88,0x11d3,{0x9a,0x16,0x00,0x90,0x27,0x3f,0xc1,0x4d}};

void *memset(void *d, int c, uintn n){ u8 *p=d; while(n--) *p++=(u8)c; return d; }
void *memcpy(void *d, const void *s, uintn n){ u8 *a=d; const u8 *b=s; while(n--) *a++=*b++; return d; }

static int guid_eq(const EFI_GUID *a, const EFI_GUID *b){
    if (a->d1!=b->d1 || a->d2!=b->d2 || a->d3!=b->d3) return 0;
    for (int i=0;i<8;i++) if (a->d4[i]!=b->d4[i]) return 0;
    return 1;
}

static SYSTEM_TABLE *ST;
static BOOT_SERVICES *BS;

static char REPORT[64*1024];
static uintn RLEN = 0;
static EFI_FILE *OUT = 0;
static uintn WRITTEN = 0;
// persistence state: 0 none/unknown, 1 ok, 2 failed
static int PERSIST = 0;

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

// Flush the unwritten tail to the file; verify status + full byte count + flush.
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

static const char *pixfmt_name(u32 f){
    switch(f){
        case 0: return "RGBReserved8 (linear)";
        case 1: return "BGRReserved8 (linear, Windows-preferred)";
        case 2: return "BitMask (linear)";
        case 3: return "BltOnly (NO linear framebuffer)";
        default: return "Unknown";
    }
}

static int GLOBAL_ANY_LINEAR = 0;

static void dump_one_gop(GOP *gop, uintn idx){
    emit("\n-- GOP handle #"); emit_u64(idx,0); emit(" --\n");
    if (!gop || !gop->Mode){ emit("  (no Mode struct)\n"); return; }
    GOP_MODE *m = gop->Mode;
    emit("  MaxMode="); emit_u64(m->MaxMode,0);
    emit("  CurrentMode="); emit_u64(m->Mode,0); emit("\n");
    emit("  FrameBufferBase="); emit_u64(m->FrameBufferBase,1);
    emit("  FrameBufferSize="); emit_u64((u64)m->FrameBufferSize,0); emit(" bytes\n");
    if (m->Info){ emit("  CurrentPixelFormat: "); emit(pixfmt_name(m->Info->PixelFormat)); emit("\n"); }
    for (u32 i=0; i<m->MaxMode; i++){
        uintn sz=0; GOP_MODE_INFO *info=0;
        if (EFI_ERROR(gop->QueryMode(gop, i, &sz, &info)) || !info) continue;
        emit("  mode "); emit_u64(i,0); emit(": ");
        emit_u64(info->HorizontalResolution,0); emit("x"); emit_u64(info->VerticalResolution,0);
        emit("  ppsl="); emit_u64(info->PixelsPerScanLine,0);
        emit("  fmt="); emit(pixfmt_name(info->PixelFormat));
        if (info->PixelFormat==2){
            emit("  R="); emit_u64(info->RedMask,1);
            emit(" G="); emit_u64(info->GreenMask,1);
            emit(" B="); emit_u64(info->BlueMask,1);
        }
        emit("\n");
        if (info->PixelFormat<=2) GLOBAL_ANY_LINEAR = 1;   // 0/1/2 are linear
        if (BS->FreePool) BS->FreePool(info);
    }
}

static void probe_gop(void){
    emit("\n== GRAPHICS OUTPUT PROTOCOL (all handles) ==\n");
    uintn nh=0; EFI_HANDLE *handles=0;
    EFI_STATUS s = BS->LocateHandleBuffer(BY_PROTOCOL, &GOP_GUID, 0, &nh, &handles);
    if (EFI_ERROR(s) || nh==0 || !handles){
        emit("LocateHandleBuffer(GOP) status "); emit_u64(s,1);
        emit("  handles="); emit_u64(nh,0); emit("\n");
        // fall back to first-instance LocateProtocol
        GOP *gop=0;
        if (!EFI_ERROR(BS->LocateProtocol(&GOP_GUID, 0, (void**)&gop)) && gop){
            emit("(fallback: single LocateProtocol instance)\n");
            dump_one_gop(gop, 0);
        } else {
            emit("=> firmware exposes NO Graphics Output Protocol at all.\n");
        }
    } else {
        emit("GOP handle count: "); emit_u64(nh,0); emit("\n");
        for (uintn i=0;i<nh;i++){
            GOP *gop=0;
            if (EFI_ERROR(BS->HandleProtocol(handles[i], &GOP_GUID, (void**)&gop)) || !gop) continue;
            dump_one_gop(gop, i);
        }
        if (BS->FreePool) BS->FreePool(handles);
    }
    emit("\nVERDICT: linear-framebuffer mode available to a loader (any handle)? ");
    emit(GLOBAL_ANY_LINEAR ? "YES\n" : "NO (BltOnly only)\n");
}

static void probe_acpi(void){
    emit("\n== ACPI TABLES ==\n");
    void *rsdp=0; const char *which="none";
    for (uintn i=0;i<ST->NumberOfTableEntries;i++){
        if (guid_eq(&ST->ConfigurationTable[i].VendorGuid, &ACPI20_GUID)){
            rsdp=ST->ConfigurationTable[i].VendorTable; which="ACPI 2.0"; break; }
    }
    if (!rsdp) for (uintn i=0;i<ST->NumberOfTableEntries;i++){
        if (guid_eq(&ST->ConfigurationTable[i].VendorGuid, &ACPI10_GUID)){
            rsdp=ST->ConfigurationTable[i].VendorTable; which="ACPI 1.0"; break; }
    }
    if (!rsdp){ emit("No ACPI RSDP in the configuration table.\n"); return; }
    emit("RSDP ("); emit(which); emit(") at "); emit_u64((u64)rsdp,1); emit("\n");
    u8 *r=(u8*)rsdp;
    emit("  signature: "); for (int i=0;i<8;i++){ char b[2]={(char)r[i],0}; emit(b); }
    emit("\n  OEMID: "); for (int i=9;i<15;i++){ char b[2]={(char)r[i],0}; emit(b); }
    u8 rev=r[15];
    emit("\n  revision: "); emit_u64(rev,0); emit("\n");
    if (rev>=2){
        u64 xsdt; memcpy(&xsdt, r+24, 8);
        emit("  XSDT at "); emit_u64(xsdt,1); emit("\n");
        u8 *x=(u8*)xsdt;
        if (x){
            u32 len; memcpy(&len, x+4, 4);
            emit("  XSDT signature: "); for(int i=0;i<4;i++){ char b[2]={(char)x[i],0}; emit(b);} emit("\n");
            if (len>=36 && len<0x100000){
                uintn count=(len-36)/8; if (count>32) count=32;
                emit("  tables ("); emit_u64(count,0); emit("): ");
                u8 *ep=x+36;
                for (uintn i=0;i<count;i++){
                    u64 taddr; memcpy(&taddr, ep+i*8, 8);
                    u8 *t=(u8*)taddr;
                    if (t){ for(int k=0;k<4;k++){ char b[2]={(char)t[k],0}; emit(b);} emit(" "); }
                }
                emit("\n  (APIC=MADT, FACP=FADT, GTDT=timers, SPCR=serial, DBG2=debug port)\n");
            }
        }
    } else emit("  (ACPI 1.0 RSDP: 32-bit RSDT only; XSDT absent)\n");
}


extern void exception_vectors(void);
extern void probe_brk(void);
extern void trap_site(void);
volatile u64 trap_state[8];
static void probe_exception(void) {
    u64 old_vbar, old_daif, current_el, sp_sel;
    __asm__ volatile("mrs %0, CurrentEL\n mrs %1, SPSel" : "=r"(current_el), "=r"(sp_sel));
    emit("CurrentEL: "); emit_u64(current_el,1); emit(" SPSel: "); emit_u64(sp_sel,1); emit("\n");
    if (current_el != 4 || sp_sel != 1) { emit("unsupported exception context\n"); return; }
    __asm__ volatile("mrs %0, vbar_el1\n mrs %1, daif" : "=r"(old_vbar), "=r"(old_daif));
    u64 vectors=(u64)(void*)exception_vectors;
    emit("expected ELR: "); emit_u64((u64)(void*)trap_site,1); emit("\n"); flush_report();
    __asm__ volatile("msr daifset, #15\n msr vbar_el1, %0\n isb" :: "r"(vectors) : "memory");
    probe_brk();
    __asm__ volatile("msr vbar_el1, %0\n isb\n msr daif, %1" :: "r"(old_vbar), "r"(old_daif) : "memory");
    emit("observed ELR: "); emit_u64(trap_state[0],1); emit("\nESR: "); emit_u64(trap_state[1],1);
    emit("\nFAR: "); emit_u64(trap_state[2],1); emit("\nSPSR: "); emit_u64(trap_state[3],1);
    emit("\nhandler SP: "); emit_u64(trap_state[4],1); emit("\noriginal VBAR: "); emit_u64(old_vbar,1);
    emit("\npreserved x16: "); emit_u64(trap_state[5],1); emit(" x17: "); emit_u64(trap_state[6],1);
    emit("\nvector VBAR: "); emit_u64(vectors,1); emit("\n");
    emit(trap_state[0] == (u64)(void*)trap_site && trap_state[1] == 0xf2000007 && trap_state[5] == 0x1234 && trap_state[6] == 0x5678 ? "EXCEPTION CONTROL PASS\n" : "EXCEPTION CONTROL FAIL\n");
    flush_report();
}

EFI_STATUS efi_main(EFI_HANDLE image, SYSTEM_TABLE *st){
    ST = st; BS = st->BootServices;

    // open report on the volume we booted from
    LOADED_IMAGE *li=0; SFS *fs=0; EFI_FILE *root=0;
    if (!EFI_ERROR(BS->HandleProtocol(image, &LOADED_IMAGE_GUID, (void**)&li)) && li &&
        !EFI_ERROR(BS->HandleProtocol(li->DeviceHandle, &SFS_GUID, (void**)&fs)) && fs &&
        !EFI_ERROR(fs->OpenVolume(fs, &root)) && root){
        c16 name[]={'E','F','I','D','I','A','G','.','T','X','T',0};
        if (EFI_ERROR(root->Open(root, &OUT, name,
                EFI_FILE_MODE_CREATE|EFI_FILE_MODE_READ|EFI_FILE_MODE_WRITE, 0))) OUT=0;
    }

    emit("cove Windows-on-VZ guest EFI diagnostic (experiment #2)\n");
    emit("=======================================================\n");
    emit("UEFI table revision: "); emit_u64(ST->Hdr.Revision>>16,0); emit(".");
    emit_u64(ST->Hdr.Revision&0xffff,0); emit("\n");
    emit("Firmware revision: "); emit_u64(ST->FirmwareRevision,1); emit("\n");
    emit("Report file open: "); emit(OUT?"yes\n":"NO (console only)\n");
    flush_report();

    probe_exception();  flush_report();


    emit("\n== DONE ==\n"); flush_report();
    if (OUT) OUT->Close(OUT);

    // console-only truth about persistence (file may be broken)
    emit("\nPERSIST: ");
    emit(PERSIST==1 ? "ok (EFIDIAG.TXT written+flushed)\n"
        : PERSIST==2 ? "FAILED (write/flush error)\n"
        : "none (no writable volume)\n");

    if (BS->Stall) BS->Stall(3ULL*1000*1000);
    return EFI_SUCCESS;
}
