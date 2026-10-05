package handler

// pricingUnavailableMessage 是「模型没有可用价格」时给客户端的提示。
// 价格缺失时扣费只会记 0 元，所以请求在入口就拒绝（见 service.CheckBillablePricing）。
const pricingUnavailableMessage = "Pricing is not configured for this model"
