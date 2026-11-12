# Security Policy

lanhop carries keystrokes between machines, so security reports are taken seriously.

## Reporting a vulnerability

**Do not open a public issue.**

Use GitHub's private reporting: go to the [Security tab](https://github.com/rodrigogaraujo/lanhop/security/advisories/new) and choose "Report a vulnerability". If you cannot use it, write to rodrigoaraujo990@gmail.com.

Include what you found, how to reproduce it and what an attacker gains from it.

You should get a first answer within 7 days. Please give us a reasonable time to release a fix before you publish details.

## Supported versions

There is no stable release yet. Fixes land on the `main` branch.

## What counts

- Reading or injecting input without having been paired
- Weaknesses in the encryption or in the pairing
- Privilege escalation through the daemon, its device permissions or its configuration files
- Crashes or memory problems triggered by data from the network
