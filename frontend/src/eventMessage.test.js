import test from "node:test";
import assert from "node:assert/strict";
import { eventMessage } from "./eventMessage.js";

test("runtime errors display as readable text without terminal colour codes", () => {
  assert.equal(eventMessage("Подписка: \u001b[31mERROR\u001b[0m [\u001b[38;5;194m1234\u001b[0m] dial tcp: i/o timeout"), "Подписка: ERROR [1234] dial tcp: i/o timeout");
  assert.equal(eventMessage("VPN route [proxy]: unavailable\nRetrying…"), "VPN route [proxy]: unavailable\nRetrying…");
  assert.equal(eventMessage(null), "");
});
