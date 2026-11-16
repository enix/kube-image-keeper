// Copy the pinned reference of an image row to the clipboard.
document.addEventListener('click', function (ev) {
  var btn = ev.target.closest && ev.target.closest('button.copy');
  if (!btn) return;
  if (!navigator.clipboard) {
    // Plain HTTP on a non-localhost origin has no clipboard API: show the
    // reference, selected, so it can be copied by hand.
    var msg = document.getElementById('msg');
    if (!msg) return;
    var box = document.createElement('div');
    box.className = 'ok';
    box.appendChild(document.createTextNode('Clipboard unavailable here; copy it by hand: '));
    var code = document.createElement('code');
    code.textContent = btn.dataset.pinned;
    box.appendChild(code);
    msg.textContent = '';
    msg.appendChild(box);
    var range = document.createRange();
    range.selectNodeContents(code);
    var sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
    return;
  }
  navigator.clipboard.writeText(btn.dataset.pinned).then(function () {
    var old = btn.textContent;
    btn.textContent = 'copied';
    setTimeout(function () { btn.textContent = old; }, 1200);
  });
});

// Clear the feedback area before every user action (non-GET request), so a
// stale message is never read as the result of the next action. GET requests
// are background polls and must not wipe the feedback.
document.addEventListener('htmx:beforeRequest', function (ev) {
  var cfg = ev.detail && ev.detail.requestConfig;
  if (cfg && cfg.verb === 'get') return;
  var msg = document.getElementById('msg');
  if (msg) msg.textContent = '';
});
