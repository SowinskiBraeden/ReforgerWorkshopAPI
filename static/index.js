var u = new URL(window.location.href);
var d = u.searchParams.get('page');
try { document.querySelector('a[href="?page='+d+'"] button').classList += (" disabled border border-2 text-secondary border-secondary border-top-0 border-end-0 border-bottom-0 rounded-0 text-start") } catch {  }
if(d) { a = './static/pages/'+d+'.md' } else { a = './static/pages/Documentation.md' }
fetch(a)
  .then(b => {
    if (!b.ok) {
      throw new Error(`Network response was not ok: ${b.status}`);
      window.location="?page=Error"
    }
    return b.text();
  })
  .then(markdownContent => {
    document.getElementById('content').innerHTML = marked.parse(markdownContent);
    document.querySelectorAll(".hl-escape").forEach(function(element) {
      element.innerHTML = element.innerHTML.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#039;");
    });
    hljs.highlightAll(document.getElementById('content'));
  })
  .catch(c => {
    console.error('Error fetching the Markdown content:', c);
    if(window.location.hostname != "127.0.0.1") {
      window.location="?page=Error"
    }
  });
