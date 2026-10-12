using System.Text.Json;

namespace AgentDock.ControlPanel;

// This same script is exercised by the live-browser runner. No private ChatGPT APIs or credentials.
internal static class GoalPageScript
{
    internal static string Build(string action, string url, string key = "", string nonce = "") =>
        "(" + Script + ")(" + JsonSerializer.Serialize(new { action, url, key, nonce, message = GoalModePolicy.ContinueMessage }) + ")";

    private const string Script = """
    function(args) {
      const out = (state, key = '', receipt = '', detail = '') => ({state, key, receipt, detail});
      if (location.origin + location.pathname !== args.url) return out('wrong_page');
      const visible = e => !!e && e.getClientRects().length > 0 &&
        getComputedStyle(e).visibility !== 'hidden' && !e.closest('[inert],[aria-hidden="true"]');
      const all = s => Array.from(document.querySelectorAll(s));
      const messages = () => {
        const modern = all('[data-chatgpt-search-unit-key]');
        return modern.length ? modern : all('[data-message-author-role]');
      };
      const role = e => e.getAttribute('data-message-author-role') ||
        (e.getAttribute('data-chatgpt-search-unit-key') || '').split(':').pop();
      const id = e => e.getAttribute('data-chatgpt-search-message-ids') || e.getAttribute('data-message-id') ||
        e.getAttribute('data-chatgpt-search-unit-key') || '';
      const text = e => (e?.innerText || '').trim();
      const msg = messages();
      const assistant = msg.filter(e => role(e) === 'assistant').at(-1);
      const users = msg.filter(e => role(e) === 'user');
      const key = JSON.stringify([msg.length, msg.slice(-4).map(id), text(assistant).slice(-16384)]);
      const editors = all('[contenteditable="true"][role="textbox"],#prompt-textarea').filter(visible);
      const editor = editors.length === 1 ? editors[0] : null;
      const buttons = all('button').filter(visible);
      const label = e => e.getAttribute('aria-label') || '';
      const stop = buttons.some(e => e.dataset.testid === 'stop-button' ||
        /^(Stop( generating| streaming| response)?|停止(生成|回答|响应)?|中止(生成)?)$/i.test(label(e)));
      const live = all('[role="status"],[aria-live]').map(text);
      const completed = live.some(t => /^(回答已完成|Response complete|Response completed)$/i.test(t)) ||
        !!assistant && buttons.some(e => /^(重新生成回复|Regenerate( response)?|Retry)$/i.test(label(e)) || e.dataset.testid === 'regenerate-response-button');
      const pending = stop || live.some(t => /^(正在(思考|生成|回答)|Thinking|Generating)(\b|[.。…\s])/i.test(t));
      const error = all('[role="alert"]').filter(visible).some(e => text(e).length > 0) ||
        buttons.some(e => /^(重试|Try again)$/i.test(label(e)));
      const shell = editor?.closest('[data-composer-body],form');
      const attachment = shell && (shell.querySelector('input[type="file"]')?.files?.length ||
        Array.from(shell.querySelectorAll('button')).some(e => /^(Remove|移除|删除).*(attachment|file|附件|文件)/i.test(label(e))) ||
        shell.querySelector('[data-testid*="attachment"], [data-attachment-id]'));
      const ready = !error && !pending && completed && editor && shell && !attachment;
      const state = error ? 'error' : pending ? 'busy' : !editor || !shell || !assistant ? 'unsupported' :
        text(editor) || attachment ? 'draft' : completed ? 'ready' : 'busy';
      if (args.action === 'observe') return out(state, key, '', document.visibilityState);
      if (args.action === 'prepare') {
        if (state !== 'ready' || key !== args.key) return out('not_sent', key);
        const prior = globalThis.__agentDockGoalV1;
        if (prior && prior.dispatched) return out('uncertain', key);
        globalThis.__agentDockGoalV1 = {nonce:args.nonce, key, users:users.map(id), dispatched:false};
        editor.focus({preventScroll:true});
        if (!document.execCommand('insertText', false, args.message) || text(editor) !== args.message)
          return out('uncertain', key, '', 'editor rejected text');
        return out('prepared', key);
      }
      const attempt = globalThis.__agentDockGoalV1;
      if (!attempt || attempt.nonce !== args.nonce) return out('uncertain', key);
      if (args.action === 'submit') {
        if (attempt.dispatched) return out('uncertain', key);
        if (!ready || text(editor) !== args.message || key !== attempt.key) return out('uncertain', key);
        const send = buttons.filter(e => e.dataset.testid === 'send-button' || /^(发送|发送消息|Send|Send message|Send prompt)$/i.test(label(e)));
        if (send.length !== 1 || send[0].disabled || send[0].getAttribute('aria-disabled') === 'true') return out('uncertain', key);
        attempt.dispatched = true;
        send[0].click();
        return out('dispatched', key);
      }
      if (args.action === 'receipt') {
        const found = users.find(e => !attempt.users.includes(id(e)) &&
          (text(e) === args.message || text(e) === '你说：\n' + args.message || text(e) === 'You said:\n' + args.message));
        if (found && attempt.dispatched && editor && !text(editor)) {
          const receipt = id(found);
          delete globalThis.__agentDockGoalV1;
          return out('sent', key, receipt);
        }
        return out('uncertain', key);
      }
      return out('unsupported', key);
    }
    """;
}
