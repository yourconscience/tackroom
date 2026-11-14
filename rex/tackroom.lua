-- tackroom actions for the Rex terminal's command palette, under "tackroom".
--
-- Put this file beside Rex's init.lua and add `require("tackroom")` to
-- init.lua; see RECIPE.md. tackroom never edits Rex config.
--
--   tackroom_workspace  runs `tackroom rex`: a tackroom session with a shell,
--                       `tackroom status` and `tackroom cron collect --table`
--   tackroom_crons      opens `tackroom cron collect --table` in a new tab
--   tackroom_view       opens the running `tackroom view` on its Crons tab,
--                       where Run now, Pause and Resume are
--
-- Rex has no panel API, so acting on a job goes through the web view.
--
-- Optional settings, after the require:
--
--   local tackroom = require("tackroom")
--   tackroom.bin = "/path/to/tackroom"         -- default: found as below; set it when several are installed
--   tackroom.config = "/path/to/tackroom.yaml" -- default: tackroom's own lookup

local M = { bin = "tackroom", config = nil }

local SHELL = "com.superlogical.terminal.shell"

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

local function read(path)
	local f = io.open(path, "r")
	if not f then
		return nil
	end
	local text = f:read("*a") or ""
	f:close()
	text = string.match(text, "^%s*(.-)%s*$")
	if text == "" then
		return nil
	end
	return text
end

rex.action({
	name = "tackroom_workspace",
	title = "Open tackroom Workspace",
	description = "Runs tackroom rex: a tackroom session with a shell, tackroom status and tackroom cron collect.",
	category = "tackroom",
	keywords = "tackroom workspace session status crons",
	run = function(ctx, args)
		local b = bin()
		local r, err = rex.exec(command(b, { "rex" }), { env = env(b), timeout = 25 })
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
		-- `tackroom rex` shows the session from outside the app, which needs
		-- Remote Control. Run from the palette, this app can show it itself.
		local list = rex.call("session.list")
		if list and list.sessions then
			for _, s in ipairs(list.sessions) do
				if s.label == "tackroom" then
					pcall(rex.client.queue, "session.select", { session_id = s.session_id })
					break
				end
			end
		end
		if last_line(r.stdout) ~= "" then
			return { output = last_line(r.stdout) }
		end
	end,
})

rex.action({
	name = "tackroom_crons",
	title = "Open tackroom Crons",
	description = "tackroom cron collect --table in a new tab: every scheduled job on every host. Act on one from the web view.",
	category = "tackroom",
	keywords = "tackroom cron crons jobs launchd collect",
	run = function(ctx, args)
		local w, err = rex.session.new_window({
			window_label = "crons",
			layout = rex.layout.block({
				flavor = SHELL,
				options = {
					command = command(bin(), { "cron", "collect", "--table" }),
					exit = { on_completion = false },
				},
			}),
		})
		if err ~= nil then
			error(err, 0)
		end
		return { window_id = w.window_id }
	end,
})

rex.action({
	name = "tackroom_view",
	title = "Open tackroom View on Crons",
	description = "The running tackroom view on its Crons tab, where Run now, Pause and Resume are, in the default browser.",
	category = "tackroom",
	keywords = "tackroom view web browser crons run pause resume",
	run = function(ctx, args)
		local state = os.getenv("XDG_STATE_HOME")
		if state == nil or state == "" then
			state = home() .. "/.local/state"
		end
		local dir = state .. "/tackroom"
		local addr, token = read(dir .. "/view.addr"), read(dir .. "/view.token")
		if not addr or not token then
			error("no tackroom view address in " .. dir .. ": start one with tackroom view", 0)
		end
		local r, err = rex.exec({ "open", "http://" .. addr .. "/?token=" .. token .. "#crons" }, { timeout = 10 })
		if r == nil then
			error(err, 0)
		end
		if not r.ok then
			error(last_line(r.stderr), 0)
		end
	end,
})

return M
