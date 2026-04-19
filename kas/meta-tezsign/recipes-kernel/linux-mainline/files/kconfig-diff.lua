#!/usr/bin/env eli
-- kconfig-diff.lua — Compare kernel config fragments and show differences.
-- Usage: eli kconfig-diff.lua <file1> <file2> [file3 ...]

local RED    = "\27[31m"
local GREEN  = "\27[32m"
local YELLOW = "\27[33m"
local CYAN   = "\27[36m"
local DIM    = "\27[2m"
local BOLD   = "\27[1m"
local RESET  = "\27[0m"

local function parse_kconfig(path)
    local configs = {}
    local f = io.open(path, "r")
    if not f then
        io.stderr:write("error: cannot open " .. path .. "\n")
        os.exit(1)
    end
    for line in f:lines() do
        -- CONFIG_FOO=y / CONFIG_FOO=n / CONFIG_FOO=m / CONFIG_FOO="string"
        local key, val = line:match("^(CONFIG_[%w_]+)=(.+)$")
        if key then
            configs[key] = val
        else
            -- # CONFIG_FOO is not set
            key = line:match("^# (CONFIG_[%w_]+) is not set$")
            if key then
                configs[key] = "n"
            end
        end
    end
    f:close()
    return configs
end

local function basename(path)
    return path:match("([^/]+)$") or path
end

local function collect_all_keys(file_configs)
    local seen = {}
    local keys = {}
    for _, configs in ipairs(file_configs) do
        for k in pairs(configs) do
            if not seen[k] then
                seen[k] = true
                keys[#keys + 1] = k
            end
        end
    end
    table.sort(keys)
    return keys
end

local function value_color(v)
    if v == nil then return DIM end
    if v == "y" then return GREEN end
    if v == "n" then return RED end
    if v == "m" then return YELLOW end
    return CYAN
end

-- ── Main ──────────────────────────────────────────────────────────────────

local args = arg
if #args < 2 then
    print("Usage: eli kconfig-diff.lua <file1> <file2> [file3 ...]")
    print()
    print("Compare kernel config fragments and highlight differences.")
    print("Recognizes CONFIG_XXX=val and '# CONFIG_XXX is not set' lines.")
    os.exit(1)
end

-- Parse all files
local files = {}
local all_configs = {}
for i = 1, #args do
    local configs = parse_kconfig(args[i])
    files[i] = { path = args[i], name = basename(args[i]), configs = configs }
    all_configs[i] = configs
end

local all_keys = collect_all_keys(all_configs)

-- Find differences
local diffs = {}
for _, key in ipairs(all_keys) do
    local values = {}
    local all_same = true
    local first_val = nil
    for i = 1, #files do
        local v = files[i].configs[key]
        values[i] = v
        if i == 1 then
            first_val = v
        elseif v ~= first_val then
            all_same = false
        end
    end
    if not all_same then
        diffs[#diffs + 1] = { key = key, values = values }
    end
end

-- Compute column widths
local name_width = 6  -- minimum "Option"
for _, d in ipairs(diffs) do
    if #d.key > name_width then name_width = #d.key end
end
name_width = name_width + 2

local col_width = 4
for i = 1, #files do
    local w = #files[i].name + 4
    if w > col_width then col_width = w end
end

-- Print header
io.write(BOLD .. "Comparing " .. #files .. " config files:" .. RESET .. "\n")
for i, f in ipairs(files) do
    io.write(string.format("  [%d] %s\n", i, f.path))
end
io.write("\n")

if #diffs == 0 then
    io.write(GREEN .. "No differences found." .. RESET .. "\n")
    os.exit(0)
end

io.write(string.format("%s%d difference(s):%s\n\n", YELLOW, #diffs, RESET))

-- Column header
local header = string.format(BOLD .. "%-" .. name_width .. "s" .. RESET, "Option")
for i = 1, #files do
    header = header .. BOLD .. string.format("  %-" .. col_width .. "s", "[" .. i .. "] " .. files[i].name) .. RESET
end
io.write(header .. "\n")
io.write(string.rep("─", name_width + (#files * (col_width + 2))) .. "\n")

-- Print diffs
for _, d in ipairs(diffs) do
    local line = string.format("%-" .. name_width .. "s", d.key)
    for i = 1, #files do
        local v = d.values[i]
        local display = v or "—"
        local color = value_color(v)
        line = line .. "  " .. color .. string.format("%-" .. col_width .. "s", display) .. RESET
    end
    io.write(line .. "\n")
end

io.write("\n" .. DIM .. "Legend: " .. GREEN .. "y" .. RESET .. DIM .. "=enabled  "
    .. RED .. "n" .. RESET .. DIM .. "=disabled  "
    .. YELLOW .. "m" .. RESET .. DIM .. "=module  "
    .. CYAN .. "val" .. RESET .. DIM .. "=other  "
    .. DIM .. "—" .. RESET .. DIM .. "=absent" .. RESET .. "\n")
