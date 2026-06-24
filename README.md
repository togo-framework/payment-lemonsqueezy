# payment-lemonsqueezy

[LemonSqueezy](https://docs.lemonsqueezy.com) driver for togo **payment**.

```bash
togo install togo-framework/payment
togo install togo-framework/payment-lemonsqueezy
```
```env
PAYMENT_DRIVER=lemonsqueezy
LEMONSQUEEZY_API_KEY=...
```

Registers on the togo `payment.PaymentProvider` interface and is selected via
`PAYMENT_DRIVER=lemonsqueezy`. Gateway API calls are scaffolded — see the LemonSqueezy docs.

MIT
