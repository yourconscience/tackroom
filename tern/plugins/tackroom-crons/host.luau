-- tackroom crons block: run `tackroom cron collect`, render host health and
-- per-host jobs, and act through `tackroom cron run|pause|resume`.
--
-- Mirrors the Herdr mod (herdr/mods/tackroom) over the same JSON contract and
-- CLI verbs, so the web view, the Herdr panel and this block agree.

local ui = tern.ui

local POLL_MS = 60 * 1000
local TIMEOUT_MS = 300 * 1000
local ACTION_MS = 120 * 1000

local HEALTH = { fail = "Failing", warn = "Last run failed", idle = "No result", ok = "Healthy", paused = "Paused" }
local ORDER = { "fail", "warn", "idle", "ok", "paused" }
local TONE = { fail = "error", warn = "warning", idle = "dim", ok = "success", paused = "muted" }
local DONE = { run = "Started", pause = "Paused", resume = "Resumed" }
local OFF = { disabled = true, not_loaded = true }

local running = {}

local function host_name(h)
	if h == "local" then
		return "This Mac"
	end
	return h
end

local function key(job)
	return job.host .. "/" .. job.source .. "/" .. job.id
end

local function retire(state)
	if running[state.cx.pane] == state then
		running[state.cx.pane] = nil
	end
	if state.timer then
		state.timer:cancel()
		state.timer = nil
	end
	state.loading = false
end

-- The registered state is the source of truth for this block run. Unlike a
-- `tern.pane.list()` membership check, it holds from the moment `init`
-- registers it, so the first collect always runs.
local function current(state)
	return running[state.cx.pane] == state
end

local function render(state)
	pcall(function()
		state.cx:render()
	end)
end

-- argv for `tackroom cron <verb> [--config cfg] <rest...>`. --config goes
-- right after the verb because Go flag parsing stops at the first positional.
local function argv_for(state, verb, rest)
	local argv = { state.bin, "cron", verb }
	if state.config and state.config ~= "" then
		table.insert(argv, "--config")
		table.insert(argv, state.config)
	end
	for _, a in rest do
		table.insert(argv, a)
	end
	return argv
end

local function run(state, args, timeout, done)
	local ok, err = pcall(function()
		tern.process.run(args, { timeout_ms = timeout }, function(res)
			done(res)
		end)
	end)
	if not ok then
		done({ status = 1, stdout = "", stderr = first_line(tostring(err)), timed_out = false })
	end
end

local function last_line(s)
	s = s or ""
	local tail = ""
	for line in string.gmatch(s, "[^\n]+") do
		tail = line
	end
	return tail
end

-- The first line of a Tern error: native failures raise with a full traceback,
-- so `tostring(err)` is multi-line and the real message is on the first line.
local function first_line(s)
	s = s or ""
	return string.match(s, "[^\n]*") or ""
end

-- The daemon's PATH is the GUI app's minimal one, so a bare `tackroom` often
-- cannot spawn. Resolve a bare name to an absolute path from the usual
-- install dirs; a path (or --bin) is used as-is.
local function resolve_bin(bin)
	if string.find(bin, "/", 1, true) then
		return bin
	end
	local home = tern.getenv("HOME")
	if home then
		for _, dir in ipairs({ home .. "/.local/bin", home .. "/go/bin" }) do
			local candidate = dir .. "/" .. bin
			local ok, exists = pcall(tern.fs.exists, candidate)
			if ok and exists then
				return candidate
			end
		end
	end
	return bin
end

local function collect(state)
	if not current(state) or state.loading then
		return
	end
	state.loading = true
	state.error = nil
	run(state, argv_for(state, "collect", {}), TIMEOUT_MS, function(res)
		if not current(state) then
			return
		end
		state.loading = false
		if res.timed_out then
			state.error = "cron collect timed out"
		elseif res.status ~= 0 then
			local err = last_line(res.stderr)
			state.error = err ~= "" and err or ("exit " .. res.status)
		else
			local ok, data = pcall(tern.json.decode, res.stdout)
			if ok and type(data) == "table" and type(data.jobs) == "table" then
				state.data = data
			else
				state.error = "tackroom printed something other than cron JSON"
			end
		end
		render(state)
	end)
end

local function poll(state)
	if not current(state) or state.timer then
		return
	end
	state.timer = tern.timer(POLL_MS, function()
		if not current(state) then
			return
		end
		state.timer = nil
		collect(state)
		poll(state)
	end)
end

local function act(state, job, verb)
	if state.busy ~= "" then
		return
	end
	state.busy = key(job)
	state.note = ""
	render(state)
	run(state, argv_for(state, verb, { "--host", job.host, "--source", job.source, job.id }), ACTION_MS, function(res)
		if not current(state) then
			return
		end
		state.busy = ""
		if res.status == 0 then
			state.note = DONE[verb] .. " " .. (job.short_name or job.name or job.id)
		else
			state.note = "Could not " .. verb .. " " .. (job.short_name or job.name or job.id) .. ": " .. last_line(res.stderr)
		end
		collect(state)
	end)
end

local WEB_CMD = [==[
d="${XDG_STATE_HOME:-$HOME/.local/state}/tackroom"
a=$(tr -d '
' < "$d/view.addr")
t=$(tr -d '
' < "$d/view.token")
open "http://$a/?token=$t#crons"
]==]

local function open_web()
	tern.process.run({ "/bin/sh", "-c", WEB_CMD }, { timeout_ms = 10000 }, function() end)
end

-- Seconds since the epoch of an ISO 8601 time with an explicit offset.
local function epoch_iso(iso)
	local y, mo, d, hh, mm, ss, sign, oh, om =
		string.match(iso, "^(%d+)-(%d+)-(%d+)T(%d+):(%d+):(%d+)([+-])(%d+):(%d+)$")
	local offset = 0
	if y then
		offset = (tonumber(oh) * 3600 + tonumber(om) * 60) * (sign == "-" and -1 or 1)
	else
		y, mo, d, hh, mm, ss = string.match(iso, "^(%d+)-(%d+)-(%d+)T(%d+):(%d+):(%d+)")
		if not y then
			return nil
		end
	end
	local year, month, day = tonumber(y), tonumber(mo), tonumber(d)
	if month <= 2 then
		year = year - 1
	end
	local era = year // 400
	local yoe = year - era * 400
	local doy = (153 * (month > 2 and month - 3 or month + 9) + 2) // 5 + day - 1
	local doe = yoe * 365 + yoe // 4 - yoe // 100 + doy
	local days = era * 146097 + doe - 719468
	return days * 86400 + tonumber(hh) * 3600 + tonumber(mm) * 60 + tonumber(ss) - offset
end

local function ago(iso)
	local t = epoch_iso(iso)
	if not t then
		return ""
	end
	local s = math.max(0, os.time() - t)
	if s < 60 then
		return "just now"
	end
	local m = s // 60
	if m < 60 then
		return m .. "m ago"
	end
	local h = m // 60
	if h < 48 then
		return h .. "h ago"
	end
	return h // 24 .. "d ago"
end

local function clock(iso)
	local hh, mm = string.match(iso, "T(%d+):(%d+)")
	if not hh then
		return iso or ""
	end
	return hh .. ":" .. mm
end

local function badge(label, act, tone)
	local node = ui.badge(label, tone)
	local p = node.p or {}
	p.actions = { click = act }
	node.p = p
	return node
end

local function section_title(text, count)
	return ui.text({ ui.span(text, "strong"), ui.span("  " .. count, "dim") })
end

local function host_line(h, data)
	local counts = {}
	for _, k in ORDER do
		local n = 0
		for _, j in data.jobs do
			if j.host == h and j.health == k then
				n = n + 1
			end
		end
		if n > 0 then
			table.insert(counts, { k, n })
		end
	end
	local spans = { ui.span(host_name(h), "strong") }
	for _, c in counts do
		table.insert(spans, ui.span("  ", "dim"))
		table.insert(spans, ui.span("● ", TONE[c[1]]))
		table.insert(spans, ui.span(c[2] .. " " .. string.lower(HEALTH[c[1]]), "muted"))
	end
	return ui.text(spans)
end

local function job_row(state, j, show_host)
	local h = j.health or "ok"
	local kids = {
		ui.text({
			ui.span("● ", TONE[h]),
			ui.span(j.short_name or j.name or j.id, h == "paused" and "muted" or nil),
			ui.span("  ", "dim"),
			ui.span(show_host and host_name(j.host) or (j.schedule_text or j.schedule or ""), "dim"),
			ui.span("  ", "dim"),
			ui.span(h == "paused" and "paused" or (j.last_run and ago(j.last_run) or "-"), "dim"),
		}),
	}
	if j.source == "launchd" and j.unit then
		local busy = state.busy == key(j)
		if OFF[j.state] then
			table.insert(kids, badge(busy and "…" or "Resume", "resume:" .. key(j), busy and "muted" or "accent"))
		else
			table.insert(kids, badge(busy and "…" or "Run now", "run:" .. key(j), busy and "muted" or "accent"))
			table.insert(kids, badge(busy and "…" or "Pause", "pause:" .. key(j), busy and "muted" or "neutral"))
		end
	end
	return ui.row(kids)
end

local function main_view(state)
	local d = state.data
	local kids = {}

	local header = {
		ui.text({
			ui.span("Crons", "strong"),
			ui.span("  " .. (state.loading and "collecting…" or (d and clock(d.scanned_at) or "")), "dim"),
			ui.span("   ", "dim"),
		}),
		badge(state.loading and "…" or "Collect", "collect", state.loading and "muted" or "accent"),
		badge("Web", "web", "neutral"),
	}
	table.insert(kids, ui.row(header))

	if state.error then
		table.insert(kids, ui.text({ ui.span(state.error, "error") }))
	end

	if not d then
		table.insert(kids, ui.progress(nil, "Collecting…"))
		return ui.col(kids)
	end

	local jobs = d.jobs or {}
	local hosts = {}
	local seen = {}
	for _, a in ipairs(d.access or {}) do
		if a.host and not seen[a.host] then
			seen[a.host] = true
			table.insert(hosts, a.host)
		end
	end
	for _, j in jobs do
		if not seen[j.host] then
			seen[j.host] = true
			table.insert(hosts, j.host)
		end
	end

	for _, h in hosts do
		table.insert(kids, host_line(h, d))
	end

	local att = {}
	for _, j in jobs do
		if j.health == "fail" then
			table.insert(att, j)
		end
	end
	if #att > 0 then
		table.insert(kids, section_title("NEEDS ATTENTION", #att))
		for _, j in att do
			table.insert(kids, job_row(state, j, true))
		end
	end

	for _, h in hosts do
		local list = {}
		for _, j in jobs do
			if j.host == h then
				table.insert(list, j)
			end
		end
		table.sort(list, function(a, b)
			local ia, ib = 0, 0
			for i, k in ipairs(ORDER) do
				if a.health == k then ia = i end
				if b.health == k then ib = i end
			end
			return ia < ib
		end)
		if #list > 0 then
			table.insert(kids, section_title(string.upper(host_name(h)), #list))
			for _, j in list do
				table.insert(kids, job_row(state, j, false))
			end
		end
	end

	return ui.col(kids)
end

local function dock_view(state)
	local lines = {}
	if state.note and state.note ~= "" then
		table.insert(lines, ui.text({ ui.span(state.note, "muted") }))
	end
	table.insert(lines, ui.text({ ui.span("Collect refreshes now · actions on launchd jobs", "dim") }))
	return ui.col({ ui.lines(lines) })
end

tern.block.define("crons", {
	init = function(cx, args, saved)
		local previous = running[cx.pane]
		if previous then
			retire(previous)
		end
		local bin = "tackroom"
		local config = ""
		if type(args) == "table" then
			for i = 1, #args - 1, 2 do
				if args[i] == "--bin" then
					bin = args[i + 1]
				elseif args[i] == "--config" then
					config = args[i + 1]
				end
			end
		end
		bin = resolve_bin(bin)
		local state = {
			cx = cx,
			bin = bin,
			config = config,
			data = nil,
			error = nil,
			loading = false,
			busy = "",
			note = "",
			timer = nil,
		}
		running[cx.pane] = state
		collect(state)
		poll(state)
		return state
	end,

	title = function(state)
		if state.data and state.data.jobs then
			return "Crons (" .. #state.data.jobs .. ")"
		end
		return "Crons"
	end,

	view = function(state, cx)
		return { main = main_view(state), dock = dock_view(state) }
	end,

	event = function(state, ev, cx)
		if ev.ev ~= "action" then
			return
		end
		local act = ev.act or ""
		if act == "collect" then
			collect(state)
		elseif act == "web" then
			open_web()
		else
			local verb, rest = string.match(act, "^(run|pause|resume):(.*)$")
			if verb and state.data then
				for _, j in state.data.jobs do
					if key(j) == rest then
						act(state, j, verb)
						break
					end
				end
			end
		end
	end,
})
