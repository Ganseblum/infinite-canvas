# Infinite Canvas Documentation Index

## Overview

- [Quick Start](/docs/overview/quick-start)
- [Features](/docs/overview/features)
- [Docker Deployment](/docs/overview/docker)
- [Third-party Prompt Sources](/docs/overview/third-party-prompt-repositories)

## Canvas Guide

- [Canvas Node Guide](/docs/canvas/canvas-node-manual)
- [Canvas Shortcuts](/docs/canvas/canvas-shortcuts)

## Development and Data

- [Project Structure](/docs/development/project-structure)
- [Local Development](/docs/development/local-development)
- [Fork Deployment and Shared Test Data](../deploy/README.md)
- [Canvas Data Structure](/docs/development/canvas-data-structure)
- [How the Local Codex Connection Works](/docs/development/local-codex-canvas)

## Business

- [Open-source License](/docs/business/license)
- [Business Cooperation](/docs/business/business)

## Support and Security

- [Report a Vulnerability](/docs/support/security)
- [Sponsor the Project](/docs/support/sponsor)

## Project Progress

- [Changelog](/docs/progress/changelog)
- [Pending Tests](/docs/progress/pending-test)
- [TODO](/docs/progress/todo)

## Notes

- On this fork's account-backend branch, Go/MySQL stores business records and local/S3 storage holds media. Test and production are independent; local development shares test data without a third database.
- AI channels are configured server-side and credentials are encrypted in the database. Full local/server Go sharing still requires media and execution-control prerequisites in the deployment guide.
- Real server details are documented in `我的规划/部署方案（测试与正式环境）.md`; it contains no passwords or keys. Environment files and actual secrets remain local-only.
