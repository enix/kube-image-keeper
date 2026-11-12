// Live request table: appends new requests, newest first, keeping at most the number of rows the server renders.
(function () {
  var table = document.getElementById('live');
  if (!table) return;
  var body = table.tBodies[0];
  var state = document.getElementById('live-state');
  var MAX = Number(table.dataset.max);

  function cell(tr, text, code) {
    var td = tr.insertCell();
    if (code) {
      var c = document.createElement('code');
      c.textContent = text;
      td.appendChild(c);
    } else {
      td.textContent = text;
    }
  }

  // The same function formats server-rendered and live rows, in local time.
  function fmtTime(iso) {
    var d = new Date(iso);
    if (isNaN(d)) return iso;
    var p = function (n, w) { return String(n).padStart(w || 2, '0'); };
    return p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds()) + '.' + p(d.getMilliseconds(), 3);
  }
  function timeCell(tr, iso) {
    var td = tr.insertCell();
    var t = document.createElement('time');
    t.setAttribute('datetime', iso);
    t.textContent = fmtTime(iso);
    td.appendChild(t);
  }
  Array.prototype.forEach.call(body.querySelectorAll('time[datetime]'), function (t) {
    t.textContent = fmtTime(t.getAttribute('datetime'));
  });
  // Without a stream the rendered rows still get local times, but no live rows.
  if (!table.dataset.stream || !window.EventSource) return;
  function row(e) {
    var tr = document.createElement('tr');
    timeCell(tr, e.time);
    cell(tr, e.host);
    cell(tr, e.clientIp);
    cell(tr, e.user);
    cell(tr, e.caller);
    cell(tr, e.method);
    cell(tr, e.kind);
    cell(tr, e.repo);
    cell(tr, e.reference);
    cell(tr, String(e.status));
    cell(tr, e.ruleId, true);
    cell(tr, e.forwarded ? 'yes' : '');
    return tr;
  }

  var es = new EventSource(table.dataset.stream);
  es.onopen = function () { if (state) state.textContent = 'live'; };
  es.onerror = function () { if (state) state.textContent = 'reconnecting'; };
  es.addEventListener('request', function (ev) {
    body.insertBefore(row(JSON.parse(ev.data)), body.firstChild);
    while (body.rows.length > MAX) body.deleteRow(body.rows.length - 1);
  });
})();
