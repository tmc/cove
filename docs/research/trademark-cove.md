# USPTO Trademark Search: COVE

Date: 2026-04-29

Scope: preliminary USPTO clearance screen for using `cove` as software / developer-tool branding. This is not a legal opinion.

## Method

- Target mark: exact word `COVE`.
- Relevant classes: International Class 009 (downloadable software) and Class 042 (SaaS / hosted software). Class 036 was noted where paired with software because cove may eventually ship paid registry/control-plane services.
- Sources used:
  - USPTO Trademark Search (`tmsearch.uspto.gov`) configuration and TSDR API.
  - USPTO TSDR serial-number records for status, filing dates, registration dates, owners, and classes.
  - TrademarkElite search results only as a serial-number discovery aid because direct non-browser POSTs to the USPTO tmsearch search endpoint were blocked by AWS WAF. Every material candidate below was checked against USPTO TSDR.

## Material candidates

| Serial | Mark | Owner | Status | Filed | Registered | Classes | Why it matters |
|---|---|---|---|---|---|---|---|
| 86034397 | COVE | LivelyHood, Inc. | Live registration, renewed | 2013-08-09 | 2016-02-23 | 009, 036, 042 | Oldest live `COVE` registration found in software/SaaS-adjacent classes. High-priority counsel review. |
| 98269732 | COVE | Eden Financial Technologies Incorporated | Live registration | 2023-11-14 | 2024-12-17 | 009, 036 | Live Class 009 registration. Appears finance-oriented, but still blocks a clean software clearance answer. |
| 98505534 | COVE | Greenfield Labs Inc. | Live, published for opposition | 2024-04-17 | none | 009 | Pending Class 009 software mark. Search snippet described AI/cognitive-computing software. |
| 98975973 | COVE | Greenfield Labs Inc. | Live, published for opposition | 2024-04-17 | none | 042 | Companion pending SaaS / virtual-assistant software mark. |
| 98883370 | COVE | Cove Labs, Inc. | Live, suspended | 2024-12-03 | none | 009 | Pending Class 009 mobile-application mark. Search snippet described downloadable journaling software. |

## Lower-risk exact-word candidates

These exact `COVE` marks were live but outside software/developer-tool classes in the first result set reviewed:

| Serial | Owner | Status | Filed | Classes |
|---|---|---|---|---|
| 99741257 | Elevate Aircraft Seating LLC | Live new application | 2026-04-02 | 012 |
| 99653797 | Laguna Creative LLC | Live new application | 2026-02-15 | 036 |
| 98586601 | DIRTT Environmental Solutions, Ltd | Live extension of time | 2024-06-05 | 006, 019, 020 |
| 98433095 | Franklin Sports, Inc. | Live registration | 2024-03-04 | 028 |
| 98293184 | Cove PBC | Live new application | 2023-11-30 | 001, 002, 016, 017, 020, 021, 022 |

## Conclusion

`cove` is not clean for software. At least three live or pending exact-word `COVE` records sit directly in Class 009, and at least two touch Class 042. The 2013 LivelyHood filing is the earliest and most important risk because it is registered and renewed.

Recommendation: do not take `cove` to a public v1 registry under this name without trademark counsel. Practical paths are:

1. Seek a legal clearance opinion and likelihood-of-confusion analysis against the Class 009/042 records above.
2. Explore coexistence or consent only if counsel thinks the goods/channels are separable.
3. Prepare an alternative product/registry mark before v1.0 so v0.3-v0.4 technical work is not blocked by a late rename.

## Source links

- USPTO Trademark Search: https://tmsearch.uspto.gov/
- USPTO TSDR 86034397: https://tmsearch.uspto.gov/tsdr-api-v1-0-0/tsdr-api?serialNumber=86034397
- USPTO TSDR 98269732: https://tmsearch.uspto.gov/tsdr-api-v1-0-0/tsdr-api?serialNumber=98269732
- USPTO TSDR 98505534: https://tmsearch.uspto.gov/tsdr-api-v1-0-0/tsdr-api?serialNumber=98505534
- USPTO TSDR 98975973: https://tmsearch.uspto.gov/tsdr-api-v1-0-0/tsdr-api?serialNumber=98975973
- USPTO TSDR 98883370: https://tmsearch.uspto.gov/tsdr-api-v1-0-0/tsdr-api?serialNumber=98883370
- TrademarkElite discovery query: https://www.trademarkelite.com/trademark/trademark-search.aspx?sw=COVE
