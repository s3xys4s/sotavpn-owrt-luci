"""Read-only inspection of Linux ELF64 and Go's embedded wire type metadata."""

import struct


def read_elf(data):
    if data[:7] != b"\x7fELF\x02\x01\x01":
        raise ValueError("expected little-endian ELF64")
    file_type, machine = struct.unpack_from("<HH", data, 16)
    phoff, shoff = struct.unpack_from("<QQ", data, 32)
    phsize, phcount, shsize, shcount, strtab = struct.unpack_from("<5H", data, 54)
    headers = [struct.unpack_from("<IIQQQQIIQQ", data, shoff + i * shsize) for i in range(shcount)]
    def cstring(offset):
        return data[offset:data.index(0, offset)].decode()
    sections = {cstring(headers[strtab][4] + h[0]): h for h in headers}
    interp = None
    for i in range(phcount):
        kind, _, offset = struct.unpack_from("<IIQ", data, phoff + i * phsize)
        if kind == 3:
            interp = cstring(offset)
    needed = []
    if ".dynamic" in sections:
        dyn = sections[".dynamic"]
        for tag, index in struct.iter_unpack("<qQ", data[dyn[4]:dyn[4] + dyn[5]]):
            if tag == 1:
                needed.append(cstring(headers[dyn[6]][4] + index))
    return dict(machine=machine, elf_type=file_type, interpreter=interp, needed=needed,
                dynamic_symbols=sections.get(".dynsym", (0,) * 10)[5] // 24), sections


def varint(data, index):
    value = 0
    for shift in range(0, 64, 7):
        byte = data[index]
        index += 1
        value |= (byte & 127) << shift
        if byte < 128:
            return value, index
    raise ValueError("invalid Go metadata integer")


def go_string(data, index):
    size, start = varint(data, index)
    if start + size > len(data):
        raise ValueError("truncated Go metadata string")
    return data[start:start + size].decode(), start + size


WIRE_TYPES = {"daemon." + name for name in ("Request", "Response", "HelloParams", "HelloResult",
              "StatusResult", "StartParams", "CheckConfigParams", "RPCError", "KillSwitchStatus")}
WIRE_TYPES.add("config.LocalOptions")


def inspect_core(data):
    header, sections = read_elf(data)
    if header["elf_type"] != 2 or header["needed"] or header["interpreter"]:
        raise ValueError("daemon runtime changed: expected a static executable")
    info = sections[".go.buildinfo"][4]
    if data[info:info + 16] != b"\xff Go buildinf:\x08\x02":
        raise ValueError("unsupported Go build information format")
    compiler, end = go_string(data, info + 32)
    length, start = varint(data, end)
    build = data[start + 16:start + length - 16].decode().splitlines()
    if "path\tgithub.com/interhive/kittencore/cmd/kittend" not in build or "build\tCGO_ENABLED=0" not in build:
        raise ValueError("unexpected Go entry point or CGO mode")
    def offset(address):
        for h in sections.values():
            if h[2] & 2 and h[1] != 8 and h[3] <= address < h[3] + h[5]:
                return h[4] + address - h[3]
        raise ValueError("unmapped Go metadata")
    ro = sections[".rodata"][4]
    def type_name(pos):
        namepos = ro + struct.unpack_from("<i", data, pos + 40)[0]
        name, _ = go_string(data, namepos + 1)
        return name[1:] if data[pos + 20] & 2 else name  # TFlagExtraStar
    types = {}
    link = sections[".typelink"]
    targets = {"*" + name for name in WIRE_TYPES}
    for (rel,) in struct.iter_unpack("<i", data[link[4]:link[4] + link[5]]):
        pos = ro + rel
        name = type_name(pos)
        if name not in targets:
            continue
        if data[pos + 23] & 31 != 22:
            raise ValueError("unexpected Go wire type")
        elem = offset(struct.unpack_from("<Q", data, pos + 48)[0])
        fields, count = struct.unpack_from("<QQ", data, elem + 56)
        if count > 100:
            raise ValueError("unexpected Go struct size")
        result = []
        for i in range(count):
            n, typ, _ = struct.unpack_from("<QQQ", data, offset(fields) + i * 24)
            idx = offset(n)
            field, end = go_string(data, idx + 1)
            tag = go_string(data, end)[0] if data[idx] & 2 else ""
            result.append([field, type_name(offset(typ)), tag])
        types[name[1:]] = result
    if set(types) != set(WIRE_TYPES):
        raise ValueError("incomplete daemon wire metadata")
    return dict(elf=header, compiler=compiler, go_build=build, wire_types=types)
