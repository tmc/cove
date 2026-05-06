async function text(path) {
  const r = await fetch(path);
  if (!r.ok) throw new Error(await r.text());
  return await r.text();
}

async function json(path) {
  const r = await fetch(path);
  if (!r.ok) throw new Error(await r.text());
  return await r.json();
}

function parseMetrics(body) {
  const out = {};
  for (const line of body.split("\n")) {
    if (!line || line[0] === "#") continue;
    const parts = line.split(/\s+/);
    out[parts[0]] = parts[1];
  }
  return out;
}

function renderSummary(status) {
  const rows = [
    ["version", status.version || ""],
    ["uptime", `${status.uptime_s || 0}s`],
    ["vms", status.vms_managed || 0],
    ["lifecycle stops", status.lifecycle_enforced || 0],
  ];
  document.getElementById("summary").innerHTML = rows.map(([k, v]) =>
    `<div class="metric"><strong>${k}</strong><br>${v}</div>`).join("");
}

function renderMetrics(metrics) {
  document.getElementById("metrics").innerHTML = Object.entries(metrics).map(([k, v]) =>
    `<tr><td><code>${k}</code></td><td>${v}</td></tr>`).join("");
}

function renderEvents(events) {
  document.getElementById("events").textContent = events.map(e => JSON.stringify(e)).join("\n");
}

async function refresh() {
  try {
    const params = new URLSearchParams(location.search);
    const fleet = params.get("mode") === "fleet";
    const status = await json("/api/status");
    renderSummary(status);
    renderMetrics(parseMetrics(await text("/metrics")));
    renderEvents(await json("/api/events"));
    if (fleet) {
      document.body.dataset.mode = "fleet";
      document.querySelector("h1").textContent = "coved fleet view";
    }
  } catch (e) {
    document.getElementById("events").textContent = e.message;
  }
}

refresh();
setInterval(refresh, 5000);
