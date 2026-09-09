export default {
  latencyCompensation: {
    title: 'Latency Compensation',
    description:
      'Refund requests that were slow to first token in a given window — bill at cost price and refund the margin. The refund ratio is controlled by the setting below.',
    from: 'From',
    to: 'To',
    thresholdMs: 'Slow-request threshold (ms)',
    thresholdHint: 'A request counts as slow once its time-to-first-token reaches this value',
    profitRatio: 'Refund ratio',
    profitRatioHint: '100% = no profit on this request, refund the full margin; 0% = no refund',
    saveSettings: 'Save settings',
    saveFailed: 'Save failed',
    preview: 'Preview',
    apply: 'Pay compensation',
    slowRequests: 'Slow requests',
    actualCost: 'Actual cost',
    accountCost: 'Account cost',
    totalCompensation: 'Total compensation',
    user: 'User',
    requests: 'Requests',
    compensation: 'Compensation',
    noneQualified: 'No requests in this window and threshold are both eligible and not yet compensated.',
    invalidRange: 'The end time must be after the start time',
    invalidThreshold: 'Threshold must be a positive integer',
    invalidRatio: 'Refund ratio must be between 0% and 100%',
    previewFailed: 'Preview failed',
    applyFailed: 'Failed to pay compensation',
    applyConfirmTitle: 'Confirm compensation payout',
    applyConfirmMessage:
      'This will credit a total of ${amount} to {count} users\' balances directly. This cannot be undone. Continue?',
    applySuccess: 'Paid a total of ${amount} to {count} users'
  }
}
