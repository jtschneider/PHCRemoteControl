using Base64

# ── Usage ────────────────────────────────────────────────────────────────────
# julia decode.jl                  → parse phc_project_raw_new.txt (ZIP report)
# julia decode.jl shutter.txt      → parse proxy capture for simInputEvent calls
# ─────────────────────────────────────────────────────────────────────────────

EVENT_NAMES = Dict(1=>"init", 2=>"press", 3=>"longPress", 4=>"release", 5=>"doublePress")

input_file = length(ARGS) > 0 ? ARGS[1] : joinpath(@__DIR__, "phc_project_raw_new.txt")
raw = read(joinpath(@__DIR__, input_file), String)

println("=== Parsing: $input_file ===\n")

# ── simInputEvent calls ───────────────────────────────────────────────────────
# Each <methodCall> block for simInputEvent
sim_re = r"<methodName>service\.stm\.simInputEvent</methodName>([\s\S]*?)</methodCall>"
sim_calls = eachmatch(sim_re, raw)
n_sim = 0
for m in sim_calls
    global n_sim += 1
    ints = [parse(Int, v) for v in eachmatch(r"<i4>(\d+)</i4>", m.captures[1]) .|> (x -> x.captures[1])]
    if length(ints) >= 5
        stm, cls, dip, evt, ch = ints[1], ints[2], ints[3], ints[4], ints[5]
        ename = get(EVENT_NAMES, evt, "unknown($evt)")
        println("simInputEvent: stm=$stm  class=$cls  dip=$dip  event=$evt($ename)  channel=$ch")
    else
        println("simInputEvent: malformed params $(ints)")
    end
end

if n_sim > 0
    println("\nTotal simInputEvent calls: $n_sim")
    return
end

# ── sendTelegram calls (state poll / light control) ───────────────────────────
tel_re = r"<methodName>service\.stm\.sendTelegram</methodName>([\s\S]*?)</methodCall>"
n_tel = 0
for m in eachmatch(tel_re, raw)
    global n_tel += 1
    ints = [parse(Int, v) for v in eachmatch(r"<i4>(\d+)</i4>", m.captures[1]) .|> (x -> x.captures[1])]
    if length(ints) >= 3
        stm, addr, content = ints[1], ints[2], ints[3]
        println("sendTelegram:  stm=$stm  addr=$addr(0x$(string(addr,base=16)))  content=$content(0x$(string(content,base=16)))")
    end
end
n_tel > 0 && println("\nTotal sendTelegram calls: $n_tel")

# ── ZIP extraction (default) ──────────────────────────────────────────────────
if n_sim == 0 && n_tel == 0
    chunks = [replace(m.captures[1], r"\s+" => "")
              for m in eachmatch(r"<base64>([\s\S]+?)</base64>", raw)]
    isempty(chunks) && (println("No recognised calls found."); return)

    zip = vcat(base64decode.(chunks)...)
    println("ZIP chunks: $(length(chunks)),  total bytes: $(length(zip))")
    println("First 8 bytes: $(join(map(b->string(b,base=16,pad=2), zip[1:min(8,end)]), " "))")

    zip_path = joinpath(@__DIR__, "project_new.zip")
    write(zip_path, zip)
    println("Saved → $zip_path")
    run(`unzip -l $zip_path`)
end
