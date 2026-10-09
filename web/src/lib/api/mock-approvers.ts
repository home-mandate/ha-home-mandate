// SPDX-License-Identifier: AGPL-3.0-or-later

// The mock's answer to GET api/templates/{name}/approvers: who may approve after an
// admission with a template, as the server works it out (internal/admission ApproversFor,
// internal/approval ReachOf). Only for the mock: the server is the authority.

import { isCritical } from '../engine/vocabulary.ts';
import { APPROVERS_PLACEHOLDER } from '../mandate/placeholder.ts';
import type { ApprovalCoverage, Approver, ApproverReach, MandateDraft, Rule, TemplateApprovers } from './types.ts';

export interface PreviewInput {
  draft: MandateDraft;
  /** Who the placeholder stands for: the admitting human, then the approvers set up. */
  placeholder: readonly string[];
  approvers: readonly Approver[];
  /** Names from Home Assistant by user ID. */
  names: Readonly<Record<string, string>>;
  self: string;
  serviceUser: string;
  /** Home Assistant cannot be asked: everyone set up is unknown. */
  haDown: boolean;
}

function expand(list: readonly string[] | undefined, placeholder: readonly string[]): string[] | undefined {
  if (list === undefined) return undefined;
  return [...new Set(list.flatMap((a) => (a === APPROVERS_PLACEHOLDER ? placeholder : [a])).filter((a) => a !== ''))];
}

/** What a rule's actions can lead to: ordinary and critical requests. */
function asks(rule: Rule): { normal: boolean; critical: boolean } {
  const category = rule.resource.category;
  return {
    normal: rule.actions.some((a) => a === '*' || category === undefined || !isCritical(category, a)),
    critical: rule.actions.some((a) => isCritical(category, a)),
  };
}

function coverage(lists: readonly (readonly string[])[], reach: (user: string) => ApproverReach): ApprovalCoverage {
  if (lists.length === 0) return 'not_needed';
  const states = lists.map((list): ApprovalCoverage => {
    const all = list.map(reach);
    if (all.some((r) => r === 'push' || r === 'ui')) return 'reachable';
    return all.includes('unknown') ? 'unknown' : 'nobody';
  });
  if (states.includes('nobody')) return 'nobody';
  return states.includes('unknown') ? 'unknown' : 'reachable';
}

export function previewApprovers(input: PreviewInput): TemplateApprovers {
  const { draft, placeholder } = input;
  const general = expand(draft.approval.approvers, placeholder) ?? [];
  const people = new Set(general);
  const normal: string[][] = [];
  const critical: string[][] = [];
  let demoted = false;
  for (const rule of draft.rules) {
    const own = expand(rule.approval?.approvers, placeholder);
    own?.forEach((p) => people.add(p));
    const kinds = asks(rule);
    if (rule.decision === 'ask') {
      if (kinds.normal) normal.push(own ?? general);
      if (kinds.critical) critical.push(own ?? general);
    } else if (rule.decision === 'allow' && kinds.critical && rule.allow_critical !== true) {
      demoted = true;
    }
  }
  if (demoted) critical.push(general);
  const reachOf = (user: string): { normal: ApproverReach; critical: ApproverReach } => {
    const set = input.approvers.find((a) => a.user_id === user);
    if (!set || user === input.serviceUser) return { normal: 'none', critical: 'none' };
    return input.haDown ? { normal: 'unknown', critical: 'unknown' } : set.reach;
  };
  return {
    people: [...people].map((user) => ({
      user_id: user,
      name: input.haDown ? null : (input.names[user] ?? null),
      ...reachOf(user),
      self: user === input.self,
      service: user === input.serviceUser,
    })),
    normal: coverage(normal, (u) => reachOf(u).normal),
    critical: coverage(critical, (u) => reachOf(u).critical),
  };
}
