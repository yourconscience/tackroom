-- Window half: a palette command and a key bind that open the crons block,
-- or focus the one already open in this window.

local KIND = "tackroom-crons.crons"

local function open_crons(cx)
	for _, pane in cx.session:panes() do
		if string.sub(pane.title, 1, #"Crons") == "Crons" then
			cx.layout:focus(pane.pane)
			return
		end
	end
	if cx:new_block(KIND, nil, "beside") == nil then
		cx:toast("error", "tackroom crons is not loaded on this host", "Install the plugin where your panes run.")
	end
end

tern.command({
	id = "open",
	title = "Open tackroom crons",
	icon = "clock",
	run = open_crons,
})

tern.bind("cmd+shift+c", "plugin.tackroom-crons.open")
