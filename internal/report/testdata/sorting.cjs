// Execute the generated report's actual script against its rendered tables.
// This minimal DOM supports precisely the APIs the script uses; row values,
// column types and table ordering come from HTML, not a copied implementation.
const assert = require('node:assert/strict');
const vm = require('node:vm');
const html = require('node:fs').readFileSync(0, 'utf8');
const text = value => value.replace(/<[^>]*>/g,'').replace(/&#39;/g,"'").replace(/&#34;/g,'"').replace(/&amp;/g,'&').replace(/&lt;/g,'<').replace(/&gt;/g,'>').trim();
const tables = Array.from(html.matchAll(/<table class="sections-table[^"]*">([\s\S]*?)<\/table>/g), match => {
  const markup = match[1];
  const headers = Array.from(markup.matchAll(/<th data-type="([^"]+)">([\s\S]*?)<\/th>/g), match => {
    const attributes = new Map();
    const button = { addEventListener(event, callback) { assert.equal(event,'click'); this.click = callback; } };
    return { label:text(match[2]), dataset:{type:match[1]}, querySelector(selector) {assert.equal(selector,'button');return button;},
      getAttribute(name) {return attributes.get(name);}, setAttribute(name,value) {attributes.set(name,value);}, removeAttribute(name) {attributes.delete(name);}, button };
  });
  const bodyMarkup = markup.match(/<tbody>([\s\S]*?)<\/tbody>/)[1];
  const rows = Array.from(bodyMarkup.matchAll(/<tr[^>]*>([\s\S]*?)<\/tr>/g), match => ({ cells:Array.from(match[1].matchAll(/<td[^>]*>([\s\S]*?)<\/td>/g), cell => ({textContent:text(cell[1])})) }));
  const body = { rows, appendChild(row) {this.rows.splice(this.rows.indexOf(row),1);this.rows.push(row);} };
  return { headers, tBodies:[body], querySelectorAll(selector) {assert.equal(selector,'thead th');return headers;} };
});
assert.ok(tables.length >= 4, 'multiple section tables plus reusable table are required');
const script = html.match(/<script>([\s\S]*?)<\/script>/)[1];
vm.runInNewContext(script, {document:{querySelectorAll(selector) {if(selector==='table.sections-table')return tables;if(selector==='nav a')return [];throw new Error(selector);}}});
const values = (table,column) => table.tBodies[0].rows.map(row=>row.cells[column].textContent);
const another = tables.find(table=>values(table,1).includes('Zebra'));
assert.ok(another);
const untouched = tables.filter(table=>table!==another).map(table=>values(table,0));
another.headers[0].button.click();assert.deepEqual(values(another,0),['2','3','10']);
another.headers[0].button.click();assert.deepEqual(values(another,0),['10','3','2']);
another.headers[1].button.click();assert.deepEqual(values(another,1),['apple','Beta','Zebra']);
another.headers[2].button.click();assert.deepEqual(values(another,3),['400','200','100']);
another.headers[2].button.click();assert.deepEqual(values(another,3),['100','200','400']);
another.headers[3].button.click();assert.deepEqual(values(another,3),['100','200','400']);
another.headers[4].button.click();assert.deepEqual(values(another,4),['10','9','Alpha']);
another.headers[4].button.click();assert.deepEqual(values(another,4),['Alpha','9','10']);
assert.deepEqual(tables.filter(table=>table!==another).map(table=>values(table,0)),untouched,'sorting affects only owning table');
assert.equal(another.headers[0].getAttribute('aria-sort'),undefined);
assert.equal(another.headers[4].getAttribute('aria-sort'),'descending');
const reusable = tables.find(table=>table.headers[0].label==='Component');
reusable.headers[2].button.click();assert.deepEqual(values(reusable,2),['10','100','400','—']);
reusable.headers[2].button.click();assert.deepEqual(values(reusable,2),['400','100','10','—']);
reusable.headers[1].button.click();assert.deepEqual(values(reusable,2),['400','100','10','—']);
reusable.headers[1].button.click();assert.deepEqual(values(reusable,2),['10','100','400','—']);
reusable.headers[4].button.click();assert.deepEqual(values(reusable,4),['10','9','Margaret Hamilton','Unknown']);
reusable.headers[4].button.click();assert.deepEqual(values(reusable,4),['Margaret Hamilton','9','10','Unknown']);
