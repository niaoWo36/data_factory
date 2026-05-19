/**
 * app.js – global state and step navigation
 */

// ── Global state ──────────────────────────────────────────────
window.AppState = {
  config: null,        // AppConfig saved from step 1
  tenantIDs: [],       // selected tenant IDs from step 2
  taskID: null,        // current migration task ID
};

// ── Step navigation ───────────────────────────────────────────
function goToStep(n) {
  document.querySelectorAll('.step-panel').forEach(p => p.classList.add('d-none'));
  document.getElementById('step' + n).classList.remove('d-none');

  document.querySelectorAll('.step-btn').forEach(btn => {
    const s = parseInt(btn.dataset.step, 10);
    btn.classList.toggle('active', s === n);
  });

  try { localStorage.setItem('df_step', n); } catch (_) {}
}

// Wire step buttons and data-go-step buttons.
document.addEventListener('DOMContentLoaded', () => {
  document.querySelectorAll('.step-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      if (!btn.disabled) goToStep(parseInt(btn.dataset.step, 10));
    });
  });
  document.querySelectorAll('[data-go-step]').forEach(btn => {
    btn.addEventListener('click', () => goToStep(parseInt(btn.dataset.goStep, 10)));
  });

  goToStep(1);
});

// ── Unlock step nav buttons ────────────────────────────────────
function unlockStep(n) {
  const btn = document.querySelector(`.step-btn[data-step="${n}"]`);
  if (btn) btn.disabled = false;
}

// ── Utility ───────────────────────────────────────────────────
function showAlert(container, type, msg) {
  const el = document.getElementById(container);
  if (!el) return;
  el.innerHTML = `<div class="alert alert-${type} py-2 mb-0">${msg}</div>`;
}

function clearAlert(container) {
  const el = document.getElementById(container);
  if (el) el.innerHTML = '';
}

async function apiPost(path, body) {
  const r = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  return r.json();
}

async function apiGet(path) {
  const r = await fetch(path);
  return r.json();
}
