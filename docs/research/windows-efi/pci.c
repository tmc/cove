// Read-only PCI inventory. Reuse the frozen diagnostic's UEFI/report helpers.
#define efi_main graphics_diagnostic_main
#include "diag.c"
#undef efi_main

// ABI: edk2 MdePkg/Include/Protocol/PciIo.h.
typedef struct PCI_IO PCI_IO;
struct PCI_IO {
    void *PollMem, *PollIo, *MemRead, *MemWrite, *IoRead, *IoWrite;
    EFI_STATUS (*Read)(PCI_IO *, u32, u32, uintn, void *);
    void *Write, *CopyMem, *Map, *Unmap, *AllocateBuffer, *FreeBuffer, *Flush;
    EFI_STATUS (*GetLocation)(PCI_IO *, uintn *, uintn *, uintn *, uintn *);
};
_Static_assert(__builtin_offsetof(PCI_IO, Read) == 48, "PCI read ABI");
_Static_assert(__builtin_offsetof(PCI_IO, GetLocation) == 112, "PCI location ABI");
static EFI_GUID PCI_GUID = {0x4cf5b200,0x68b8,0x4ca5,{0x9e,0xec,0xb2,0x3e,0x3f,0x50,0x02,0x9a}};

static void probe_pci(void) {
    uintn n = 0;
    EFI_HANDLE *handles = 0;
    EFI_STATUS s = BS->LocateHandleBuffer(BY_PROTOCOL, &PCI_GUID, 0, &n, &handles);
    emit("PCI enumeration status="); emit_u64(s,1);
    emit(" handles="); emit_u64(n,0); emit("\n"); flush_report();
    if (EFI_ERROR(s) || !handles) return;
    for (uintn i=0; i<n; i++) {
        PCI_IO *pci = 0;
        s = BS->HandleProtocol(handles[i], &PCI_GUID, (void **)&pci);
        emit("handle "); emit_u64(i,0); emit(" protocol status="); emit_u64(s,1); emit("\n");
        if (EFI_ERROR(s) || !pci) { flush_report(); continue; }
        uintn seg=0,bus=0,dev=0,fn=0;
        s = pci->GetLocation(pci,&seg,&bus,&dev,&fn);
        emit(" location status="); emit_u64(s,1);
        if (!EFI_ERROR(s)) {
            emit(" segment:bus:device:function="); emit_u64(seg,1); emit(":");
            emit_u64(bus,1); emit(":"); emit_u64(dev,1); emit(":"); emit_u64(fn,1);
        }
        emit("\n");
        u32 cfg[64] = {0};
        s = pci->Read(pci,2,0,64,cfg);
        emit(" config read status="); emit_u64(s,1); emit("\n");
        if (EFI_ERROR(s)) { flush_report(); continue; }
        emit(" vendor="); emit_u64(cfg[0]&0xffff,1);
        emit(" device="); emit_u64(cfg[0]>>16,1);
        emit(" command="); emit_u64(cfg[1]&0xffff,1); emit("\n");
        // Raw config preserves capabilities and BAR pairs without sizing writes.
        for (u32 j=0; j<64; j+=4) {
            emit(" "); emit_u64(j*4,1); emit(":");
            for (u32 k=0; k<4; k++) { emit(" "); emit_u64(cfg[j+k],1); }
            emit("\n");
        }
        flush_report();
    }
    BS->FreePool(handles);
}

EFI_STATUS efi_main(EFI_HANDLE image, SYSTEM_TABLE *st) {
    ST=st; BS=st->BootServices;
    LOADED_IMAGE *li=0; SFS *fs=0; EFI_FILE *root=0;
    if (!EFI_ERROR(BS->HandleProtocol(image,&LOADED_IMAGE_GUID,(void **)&li)) && li &&
        !EFI_ERROR(BS->HandleProtocol(li->DeviceHandle,&SFS_GUID,(void **)&fs)) && fs &&
        !EFI_ERROR(fs->OpenVolume(fs,&root)) && root) {
        c16 name[]={'P','C','I','D','I','A','G','.','T','X','T',0};
        if (EFI_ERROR(root->Open(root,&OUT,name,EFI_FILE_MODE_CREATE|EFI_FILE_MODE_READ|EFI_FILE_MODE_WRITE,0))) OUT=0;
    }
    emit("cove EFI PCI inventory; read-only config; use fresh image per run\n");
    emit("report open="); emit(OUT?"yes\n":"no\n"); flush_report();
    probe_pci();
    emit("DONE; missing PCI handles do not prove missing hardware\n"); flush_report();
    if (OUT) OUT->Close(OUT);
    if (root) root->Close(root);
    emit("PERSIST: "); emit(PERSIST==1?"ok\n":PERSIST==2?"FAILED\n":"none\n");
    BS->Stall(3000000);
    return EFI_SUCCESS;
}
