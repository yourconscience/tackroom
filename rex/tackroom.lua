-- tackroom actions for the Rex terminal's command palette, under "tackroom".
--
-- Put this file beside Rex's init.lua and add `require("tackroom")` to
-- init.lua; see RECIPE.md. tackroom never edits Rex config.
--
--   tackroom_panel  runs `tackroom rex`: shows or hides the tackroom panel, a
--                   floating `tackroom dash` at the right of this window
--   tackroom_view   opens `tackroom view` on its Crons tab, starting it
--                   when none runs
--
-- Optional settings, after the require:
--
--   local tackroom = require("tackroom")
--   tackroom.bin = "/path/to/tackroom"         -- default: found as below; set it when several are installed
--   tackroom.config = "/path/to/tackroom.yaml" -- default: tackroom's own lookup

local M = { bin = "tackroom", config = nil }

local function home()
	return os.getenv("HOME") or ""
end

local function exists(path)
	local f = io.open(path, "r")
	if f then
		f:close()
		return true
	end
	return false
end

-- Rex's server runs programs with the GUI app's minimal PATH, so a bare
-- `tackroom` is often not on it. A bare name is looked up on that PATH, then in
-- ~/.local/bin, ~/go/bin and Homebrew's bin folders; the first one found wins.
-- A path is used as is.
local function bin()
	local b = M.bin
	if string.find(b, "/", 1, true) then
		return b
	end
	local dirs = {}
	for dir in string.gmatch(os.getenv("PATH") or "", "[^:]+") do
		table.insert(dirs, dir)
	end
	table.insert(dirs, home() .. "/.local/bin")
	table.insert(dirs, home() .. "/go/bin")
	table.insert(dirs, "/opt/homebrew/bin")
	table.insert(dirs, "/usr/local/bin")
	for _, dir in ipairs(dirs) do
		if exists(dir .. "/" .. b) then
			return dir .. "/" .. b
		end
	end
	return b
end

-- argv for `tackroom <words...> [--config cfg]`.
local function command(b, words)
	local argv = { b }
	for _, w in ipairs(words) do
		table.insert(argv, w)
	end
	if M.config and M.config ~= "" then
		table.insert(argv, "--config")
		table.insert(argv, M.config)
	end
	return argv
end

-- `tackroom rex` runs the `rex` CLI, which Rex links into ~/.local/bin, so that
-- folder and tackroom's own go in front of the server's PATH.
local function env(b)
	local dirs = {}
	local dir = string.match(b, "^(.*)/[^/]*$")
	if dir then
		table.insert(dirs, dir)
	end
	table.insert(dirs, home() .. "/.local/bin")
	table.insert(dirs, os.getenv("PATH") or "/usr/bin:/bin:/usr/sbin:/sbin")
	return { PATH = table.concat(dirs, ":") }
end

local function last_line(s)
	local tail = ""
	for line in string.gmatch(s or "", "[^\n]+") do
		if string.match(line, "%S") then
			tail = line
		end
	end
	return tail
end

rex.action({
	name = "tackroom_panel",
	title = "Toggle tackroom Panel",
	description = "Shows or hides the tackroom panel at the right of this window: tackroom dash, with the sync state and the crons. q in the panel closes it.",
	category = "tackroom",
	keywords = "tackroom panel dash status sync crons jobs",
	run = function(ctx, args)
		if not ctx.session_id then
			error("Toggle tackroom Panel needs a Rex session", 0)
		end
		local b = bin()
		-- `tackroom rex` finds its session and window by the variables Rex
		-- sets in a pane; here they come from where the action was run.
		local e = env(b)
		e.REX_SESSION = ctx.session_id
		e.REX_BLOCK = ctx.block_id or false
		local r, err = rex.exec(command(b, { "rex" }), { env = e, timeout = 25 })
		if r == nil then
			error(err, 0)
		end
		if not r.ok then
			local reason = last_line(r.stderr)
			if reason == "" then
				reason = last_line(r.stdout)
			end
			error("tackroom rex: " .. reason, 0)
		end
	end,
})

rex.action({
	name = "tackroom_view",
	title = "Open tackroom View on Crons",
	description = "The running tackroom view on its Crons tab, where Run now, Pause and Resume are, in the default browser.",
	category = "tackroom",
	keywords = "tackroom view web browser crons run pause resume",
	run = function(ctx, args)
		-- `tackroom view --json` starts the view when none runs and prints its signed-in URL.
		local r, err = rex.exec(command(bin(), { "view", "--json" }), { timeout = 20 })
		if r == nil then
			error(err, 0)
		end
		if not r.ok then
			if string.find(r.stderr or "", "flag provided but not defined", 1, true) then
				error("Web needs a newer tackroom; upgrade it with brew or npm", 0)
			end
			error("tackroom view: " .. last_line(r.stderr), 0)
		end
		local url = string.match(r.stdout or "", '"url"%s*:%s*"([^"]+)"')
		if not url then
			error("tackroom view --json printed no url", 0)
		end
		r, err = rex.exec({ "open", url .. "#crons" }, { timeout = 10 })
		if r == nil then
			error(err, 0)
		end
		if not r.ok then
			error(last_line(r.stderr), 0)
		end
	end,
})

return M
