# API pricing overrides

`copilot-show` keeps public token pricing in the repository, but it should not hard-code customer-specific enterprise contracts.

The pricing research pointed to a cleaner split:

- public list pricing belongs in the built-in catalog,
- account-specific effective prices belong in a local override file,
- the repo should document the shape of the problem, not embed NDA-bound commercial terms.

That is why `stats --api-costs` now supports:

```bash
copilot-show stats --api-costs
copilot-show stats --api-pricing-template > ~/.copilot/api-pricing.yaml
copilot-show stats --api-pricing ~/.copilot/api-pricing.yaml
```

## Why a single built-in discount rate is not enough

The public research did not support one universal `discountRate` knob.

Instead, it showed several distinct pricing shapes:

- GitHub Copilot public discounts are mostly entitlement-style seat concessions, such as student, teacher, and maintainer access.
- Model providers publish rate-class modifiers such as batch pricing, cached-input pricing, and priority surcharges.
- Google Cloud contract pricing surfaces as effective price, contract price, anchored vs. floating discount logic, and exclusion state.
- Some offerings can be explicitly ineligible for discount programs.

So the durable repository design is:

- preserve a public default catalog,
- let users override effective prices locally,
- keep private contract terms out of the repository.

## Why local YAML is the right surface

The public Google Cloud pricing docs are especially explicit that contract pricing is account-specific.

They expose concepts such as:

- `billing_account_price`,
- `FIXED_DISCOUNT`,
- `FLOATING_DISCOUNT`,
- `LIST_PRICE_AS_CEILING`,
- effective discount per SKU in the Pricing report.

That makes a local effective-price override more trustworthy than trying to encode one generic enterprise-discount feature in the repo itself.

For `copilot-show`, local YAML is also operationally simpler:

- it is easy to keep private,
- it can model exact effective prices instead of guessed percentages,
- it works even when the underlying commercial terms are not publicly documented.

## Template behavior

`--api-pricing-template` prints a commented starter file that includes every built-in model and every currently known field:

- `inputUsdPerMToken`
- `cacheReadUsdPerMToken`
- `cacheWriteUsdPerMToken`
- `outputUsdPerMToken`

The template is intentionally comment-only so users can start from the built-in catalog without editing noise.

Notable rules:

- omitted fields inherit the built-in catalog,
- new models must define at least input and output prices,
- a comment-only template is a valid no-op override file,
- `source` is intentionally omitted from the template because it adds noise for the local effective-price override use case.

## Minimal example

```yaml
models:
  gpt-5.4:
    inputUsdPerMToken: 1.50
    outputUsdPerMToken: 12.00
  claude-opus-4.6:
    inputUsdPerMToken: 4.00
    outputUsdPerMToken: 20.00
```

This is meant to be a practical local editing surface, not a public contract-format standard.
