import { test } from 'node:test';
import assert from 'node:assert/strict';
import { countLabel, formatHistoryDate, historyTranslationText } from './history-display.ts';
test('dates handle SQLite timestamps and unavailable dates', () => {
  assert.equal(formatHistoryDate(), 'an unknown date');
  assert.equal(formatHistoryDate('not a timestamp'), 'an unknown date');
  const date = formatHistoryDate('2026-07-07 07:30:14+00:00', false);
  assert.match(date, /2026/);
  assert.doesNotMatch(date, /00:00|T07/);
});

test('counts are readable for one or several conversations', () => {
  assert.equal(countLabel(1, 'conversation'), '1 conversation');
  assert.equal(countLabel(3, 'message'), '3 messages');
});

test('translations show text without rendering internal JSON', () => {
  assert.equal(historyTranslationText({text: 'Synthetic translation', model: 'internal'}), 'Synthetic translation');
  assert.equal(historyTranslationText(null), null);
  assert.equal(historyTranslationText({text: 7}), null);
});
