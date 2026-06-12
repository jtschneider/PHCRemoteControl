using Base64

raw = read(joinpath(@__DIR__, "phc_project_raw_new.txt"), String)

chunks = [replace(m.captures[1], r"\s+" => "")
          for m in eachmatch(r"<base64>([\s\S]+?)</base64>", raw)]

println("readFile chunks found: $(length(chunks))")

zip = vcat(base64decode.(chunks)...)
println("Total ZIP bytes: $(length(zip))")
println("First 8 bytes (hex): $(join(map(b -> string(b, base=16, pad=2), zip[1:min(8,end)]), " "))")
println()

function u16le(d, i); d[i] | UInt16(d[i+1]) << 8; end
function u32le(d, i); d[i] | UInt32(d[i+1]) << 8 | UInt32(d[i+2]) << 16 | UInt32(d[i+3]) << 24; end

let off = 1, n = 0
    while off + 29 <= length(zip)
        sig = zip[off:off+3]
        if sig != UInt8[0x50, 0x4B, 0x03, 0x04]
            println("No PK local header at offset $(off-1); sig=$(join(map(b->string(b,base=16,pad=2),sig)," "))")
            break
        end
        n += 1
        flags       = u16le(zip, off+6)
        compression = u16le(zip, off+8)
        crc32       = u32le(zip, off+14)
        csize       = u32le(zip, off+18)
        usize       = u32le(zip, off+22)
        namelen     = u16le(zip, off+26)
        extralen    = u16le(zip, off+28)
        name        = String(copy(zip[off+30 : off+29+namelen]))
        data_start  = off + 30 + namelen + extralen

        println("--- Entry $n: $name ---")
        println("  flags       = 0x$(string(flags,base=16,pad=4))  (bit3=$(Bool((flags>>3)&1))  bit11=$(Bool((flags>>11)&1)))")
        println("  compression = $compression  (0=stored, 8=deflate)")
        println("  crc32       = 0x$(string(crc32,base=16,pad=8))")
        println("  csize       = $csize")
        println("  usize       = $usize")
        println("  data_start  = $(data_start-1)  (0-indexed)")

        if (flags & 0x08) != 0
            # csize/usize in header are 0; find data descriptor after the stream
            # scan forward for PK\x07\x08 signature
            found_dd = data_start - 1
            for i in data_start : length(zip)-3
                if zip[i:i+3] == UInt8[0x50,0x4B,0x07,0x08]
                    found_dd = i
                    break
                end
            end
            if found_dd > data_start - 1
                real_csize = u32le(zip, found_dd+4)
                real_usize = u32le(zip, found_dd+8)
                println("  data-descriptor at $(found_dd-1): real csize=$real_csize  usize=$real_usize")
                first4 = zip[data_start:data_start+3]
                println("  first 4 deflate bytes: $(join(map(b->string(b,base=16,pad=2),first4)," "))")
                off = found_dd + 16
            else
                println("  data-descriptor NOT FOUND — cannot advance")
                break
            end
        else
            data_end = data_start + Int(csize) - 1
            if compression == 8 && csize > 0 && data_end <= length(zip)
                first4 = zip[data_start:data_start+3]
                println("  first 4 deflate bytes: $(join(map(b->string(b,base=16,pad=2),first4)," "))")
            end
            off = data_end + 1
        end
        println()
    end
    println("Entries found: $n")
end

zip_path = joinpath(@__DIR__, "project_new.zip")
write(zip_path, zip)
println("ZIP saved => $zip_path")
run(`unzip -l $zip_path`)
