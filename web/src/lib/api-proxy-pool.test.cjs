const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");
const ts = require("typescript");

// Exercise the real API methods with fixture HTTP responses, without a running
// backend or Next.js. Only the optional demo transport is replaced.
const compiled = ts.transpileModule(fs.readFileSync(path.join(__dirname, "api.ts"), "utf8"), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
}).outputText;

function client(payload) {
  const exports = {};
  const modules = {};
  const context = {
    exports,
    process: { env: {} },
    URLSearchParams,
    AbortController, DOMException, setTimeout, clearTimeout,
    require: (id) => {
      if (id === "@/lib/mock/enabled") return { MOCK: false };
      if (id === "@/lib/mock/handler") return { mockHandle: () => assert.fail("unexpected demo request") };
      // 使用真实请求与轮询模块，保留本分叉的取消和超时语义。
      if (id === "./request" || id === "./polling") {
        if (!modules[id]) {
          modules[id] = {};
          const source = fs.readFileSync(path.join(__dirname, `${id}.ts`), "utf8");
          const code = ts.transpileModule(source, {
            compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
          }).outputText;
          vm.runInNewContext(code, { ...context, exports: modules[id] });
        }
        return modules[id];
      }
      throw new Error(`Unexpected runtime import: ${id}`);
    },
    fetch: async () => ({ ok: true, status: 200, json: async () => payload }),
  };
  vm.runInNewContext(compiled, context);
  return exports.api;
}

for (const method of ["proxyPool", "importProxyPool", "testAllProxyPool"]) {
  for (const value of [null, undefined]) {
    test(`${method} normalizes ${value} collections before settings renders them`, async () => {
      const result = await client({ entries: value, switch_log: value, errors: value, active_id: "" })[method]({});
      assert.ok(Array.isArray(result.entries));
      assert.equal(result.entries.length, 0);
      assert.ok(Array.isArray(result.switch_log));
      assert.equal(result.switch_log.length, 0);
      if (method === "importProxyPool") {
        assert.ok(Array.isArray(result.errors));
        assert.equal(result.errors.length, 0);
      }
    });
  }

  test(`${method} preserves proxy entries, switch history and active selection`, async () => {
    const payload = {
      entries: [{ id: "proxy-a", enabled: true }],
      switch_log: [{ at: "2026-11-02T08:00:00Z", to: "proxy-a", reason: "manual" }],
      errors: [{ line: 2, error: "invalid URL" }],
      active_id: "proxy-a",
    };
    const result = await client(payload)[method]({});
    assert.equal(result.entries, payload.entries);
    assert.equal(result.switch_log, payload.switch_log);
    assert.equal(result.active_id, "proxy-a");
    if (method === "importProxyPool") assert.equal(result.errors, payload.errors);
  });
}
