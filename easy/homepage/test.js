const jsdom = require('jsdom');
const { JSDOM } = jsdom;
const fs = require('fs');
const path = require('path');

const html = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8');
const dom = new JSDOM(html, { runScripts: 'dangerously', url: 'http://localhost' });
const document = dom.window.document;

// Simulate user input
document.querySelectorAll('.field').forEach((field, i) => {
  field.value = field.dataset.expected;
});

// Simulate click
document.getElementById('submit').click();

console.log('After correct password:');
console.log('  href:', dom.window.location.href);

// Reset and test wrong password
document.querySelectorAll('.field').forEach(field => {
  field.value = '9';
});

document.getElementById('submit').click();

console.log('After wrong password:');
console.log('  href:', dom.window.location.href);
console.log('  fields cleared:', document.querySelectorAll('.field').every(f => f.value === ''));
