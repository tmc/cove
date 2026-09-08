"""Map file extents in a GPT/FAT32 scratch image (512-byte GPT sectors)."""
import argparse
import json
import struct


def extents(image):
    with open(image, "rb") as disk:
        def read(offset, size):
            disk.seek(offset)
            data = disk.read(size)
            if len(data) != size:
                raise ValueError("short image read")
            return data
        header = read(512, 512)
        if header[:8] != b"EFI PART":
            raise ValueError("expected GPT")
        table, count, size = struct.unpack_from("<QII", header, 72)
        if count > 4096 or size < 128 or size > 4096:
            raise ValueError("invalid GPT entry layout")
        entries = read(table * 512, count * size)
        result = []
        for index in range(count):
            entry = entries[index * size:(index + 1) * size]
            if not any(entry[:16]):
                continue
            first, last = struct.unpack_from("<QQ", entry, 32)
            base = first * 512
            boot = read(base, 512)
            if boot[82:90] != b"FAT32   ":
                raise ValueError("non-FAT32 partition requires another mapper")
            sector = struct.unpack_from("<H", boot, 11)[0]
            spc = boot[13]
            reserved = struct.unpack_from("<H", boot, 14)[0]
            fats = boot[16]
            fatsectors = struct.unpack_from("<I", boot, 36)[0]
            root = struct.unpack_from("<I", boot, 44)[0]
            if sector not in (512, 1024, 2048, 4096) or not spc or spc & (spc - 1):
                raise ValueError("invalid FAT geometry")
            clusterbytes = sector * spc
            fat = read(base + reserved * sector, fatsectors * sector)
            start = base + (reserved + fats * fatsectors) * sector
            def chain(cluster):
                seen = set()
                while cluster >= 2 and cluster < 0x0ffffff8:
                    if cluster in seen or cluster * 4 + 4 > len(fat):
                        raise ValueError("invalid FAT chain")
                    seen.add(cluster)
                    offset = start + (cluster - 2) * clusterbytes
                    if offset + clusterbytes > (last + 1) * 512:
                        raise ValueError("cluster outside partition")
                    yield offset
                    cluster = struct.unpack_from("<I", fat, cluster * 4)[0] & 0x0fffffff
                if cluster < 0x0ffffff8:
                    raise ValueError("unterminated FAT chain")
            seen_dirs = set()
            def walk(cluster, path):
                if cluster in seen_dirs:
                    raise ValueError("directory loop")
                seen_dirs.add(cluster)
                longname = {}
                for offset in chain(cluster):
                    block = read(offset, clusterbytes)
                    for pos in range(0, clusterbytes, 32):
                        ent = block[pos:pos + 32]
                        if ent[0] == 0:
                            return
                        if ent[0] == 0xe5:
                            longname = {}
                            continue
                        if ent[11] == 15:
                            if ent[0] & 0x40:
                                longname = {}
                            longname[ent[0] & 31] = ent[1:11] + ent[14:26] + ent[28:32]
                            continue
                        if ent[11] & 8:
                            longname = {}
                            continue
                        short = ent[:8].decode("ascii").rstrip()
                        suffix = ent[8:11].decode("ascii").rstrip()
                        name = short + (("." + suffix) if suffix else "")
                        if longname:
                            name = b"".join(longname[k] for k in sorted(longname)).decode("utf-16le").split("\0")[0].rstrip("\uffff")
                        longname = {}
                        if name in (".", ".."):
                            continue
                        child = (struct.unpack_from("<H", ent, 20)[0] << 16) | struct.unpack_from("<H", ent, 26)[0]
                        length = struct.unpack_from("<I", ent, 28)[0]
                        full = path + "/" + name
                        if ent[11] & 16:
                            walk(child, full)
                        elif length:
                            fileoffset = 0
                            for physical in chain(child):
                                used = min(clusterbytes, max(0, length - fileoffset))
                                if used:
                                    if result and result[-1]["path"] == full and result[-1]["offset"] + result[-1]["length"] == physical:
                                        result[-1]["length"] += used
                                    else:
                                        result.append({"partition": index, "path": full, "offset": physical, "length": used, "file_offset": fileoffset})
                                fileoffset += clusterbytes
                            if fileoffset < length:
                                raise ValueError("file chain shorter than file")
            walk(root, "")
        return result


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("image")
    args = parser.parse_args()
    print(json.dumps(extents(args.image), indent=2))
