import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

// tackroom inside Pi: `/tackroom` lists the sync status and the same
// `tackroom cron collect` JSON the web view and the Herdr panel draw. Picking
// a job shows its details and acts through `tackroom cron run|pause|resume`.

type Job = {
  host: string;
  source: string;
  id: string;
  unit?: string;
  state?: string;
  last_run?: string;
  last_exit?: number;
  last_status?: string;
  problems?: string[];
  health: string;
  short_name: string;
  schedule_text: string;
};

// A launchd job that is off offers Resume, as in the web view.
const OFF = ["disabled", "not_loaded"];
const ORDER = ["fail", "warn", "idle", "ok", "paused"];
const HEALTH: Record<string, string> = {
  fail: "failing",
  warn: "last run failed",
  idle: "no result",
  ok: "healthy",
  paused: "paused",
};
const VERBS: Record<string, string> = { "Run now": "run", Pause: "pause", Resume: "resume" };
const DONE: Record<string, string> = { run: "Started", pause: "Paused", resume: "Resumed" };

const hostName = (host: string) => (host === "local" ? "This Mac" : host);
const lastLine = (ran: { stdout: string; stderr: string; code: number }) =>
  (ran.stderr || ran.stdout).trim().split("\n").pop() || `exit ${ran.code}`;

function ago(iso: string | undefined) {
  const at = iso ? Date.parse(iso) : NaN;
  if (Number.isNaN(at)) return "-";
  const minutes = Math.round((Date.now() - at) / 60e3);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  return hours < 48 ? `${hours} h ago` : `${Math.round(hours / 24)} d ago`;
}

export default function (pi: ExtensionAPI) {
  // `status --json` exits 1 when a harness is out of sync, 2 on an error.
  async function collect() {
    const [status, crons] = await Promise.all([
      pi.exec("tackroom", ["status", "--json"], { timeout: 60_000 }),
      pi.exec("tackroom", ["cron", "collect"], { timeout: 300_000 }),
    ]);
    if (status.code > 1) throw new Error(lastLine(status));
    if (crons.code !== 0) throw new Error(lastLine(crons));
    const agents: { name: string; detected: boolean; synced: boolean }[] = JSON.parse(status.stdout).agents ?? [];
    const jobs: Job[] = JSON.parse(crons.stdout).jobs ?? [];
    jobs.sort((a, b) => ORDER.indexOf(a.health) - ORDER.indexOf(b.health));
    return { jobs, drifted: agents.filter((a) => a.detected && !a.synced).map((a) => a.name) };
  }

  pi.registerCommand("tackroom", {
    description: "Show tackroom sync status and scheduled jobs",
    handler: async (_args, ctx) => {
      if (!ctx.hasUI) return;
      // Collected again only after an action, so Back returns at once.
      let data: Awaited<ReturnType<typeof collect>> | undefined;
      for (;;) {
        try {
          data ??= await collect();
        } catch (error) {
          ctx.ui.notify(`tackroom: ${error instanceof Error ? error.message : String(error)}`, "error");
          return;
        }
        const sync = data.drifted.length ? `out of sync: ${data.drifted.join(", ")} (run tackroom sync)` : "in sync";
        const failing = data.jobs.filter((job) => job.health === "fail").length;
        if (data.jobs.length === 0) {
          ctx.ui.notify(`tackroom ${sync}, no scheduled jobs`, "info");
          return;
        }

        // The number keeps two jobs with the same name and host apart.
        const rows = data.jobs.map((job, i) => {
          const when = job.health === "paused" ? "paused" : ago(job.last_run);
          return `${i + 1}. ${job.short_name} [${HEALTH[job.health] ?? job.health}] ${hostName(job.host)} · ${job.schedule_text} · ${when}`;
        });
        const picked = await ctx.ui.select(`tackroom ${sync} · ${data.jobs.length} jobs, ${failing} failing`, rows);
        const job = picked === undefined ? undefined : data.jobs[rows.indexOf(picked)];
        if (!job) return;

        const result = job.last_exit != null ? `exit ${job.last_exit}` : job.last_status;
        const title = [job.id, HEALTH[job.health] ?? job.health, result, ...(job.problems ?? [])].filter(Boolean).join(" · ");
        const actions =
          job.source === "launchd" && job.unit ? (OFF.includes(job.state ?? "") ? ["Resume"] : ["Run now", "Pause"]) : [];
        const verb = VERBS[(await ctx.ui.select(title, [...actions, "Back"])) ?? ""];
        if (!verb) continue;

        const ran = await pi.exec("tackroom", ["cron", verb, "--host", job.host, "--source", job.source, job.id], {
          timeout: 120_000,
        });
        if (ran.code === 0) ctx.ui.notify(`${DONE[verb]} ${job.short_name}`, "info");
        else ctx.ui.notify(`Could not ${verb} ${job.short_name}: ${lastLine(ran)}`, "error");
        data = undefined;
      }
    },
  });
}
