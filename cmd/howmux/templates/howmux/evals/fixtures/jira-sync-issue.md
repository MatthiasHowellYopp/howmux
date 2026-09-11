# Issue: Sync status to Jira ticket AEA-123

## Problem
We need to synchronize Howmux workflow completion status back to the associated Jira ticket for cross-team visibility.

## User Story
As a project manager, I want Howmux to automatically update the linked Jira ticket (AEA-123) when an issue is completed, so I can track progress without checking multiple systems.

## Acceptance Criteria
- [ ] Krew-lead reads the Jira ticket AEA-123 before starting work
- [ ] After successful PR creation, adds a comment to AEA-123 with the PR link
- [ ] Comment includes completion timestamp and PR URL
- [ ] Uses jtk CLI for all Jira interactions

## Context
This ticket is tracked as AEA-123 in Jira and needs bi-directional sync with GitHub workflows.

## Technical Notes
- Use `jtk issues get AEA-123` to verify ticket exists
- Use `jtk comments add AEA-123 --body "..."` to post completion status
- Ensure jtk CLI is available in the environment
