"""Conservative field classification shared by marks and action receipts."""

# Inspect metadata only; never return the field value. Unknown targets are private.
SENSITIVE_ELEMENT = r"""e => {
    if (!e || !(e.matches('input,textarea') || e.isContentEditable)) return true;
    const metadata = ['type', 'name', 'id', 'autocomplete', 'aria-label', 'placeholder', 'data-sensitive']
        .map(k => e.getAttribute(k) || '').join(' ') + ' ' +
        Array.from(e.labels || []).map(l => l.textContent || '').join(' ');
    return e.hasAttribute('data-sensitive') ||
        /password|passwd|pwd|secret|token|api.?key|credential|otp|one.?time|verification|security.?code|cc-|credit|card|cvv|cvc|pin|ssn|密码|口令|密钥|验证码|银行卡|身份证/i.test(metadata);
}"""
