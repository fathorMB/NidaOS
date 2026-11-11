# Writing rules

These rules are mandatory. They apply to the lead agent and to all sub-agents.

## Which language to use

| You write | Language |
| --- | --- |
| Messages to the operator in the session | Italian or English. Use the language of the last message from the operator. For other languages, use English. |
| All text in `.lmbrain-lite/` | ASD-STE100 Simplified Technical English. |
| Source code: names, comments, doc comments, log and error messages | ASD-STE100 Simplified Technical English. |
| Project documentation: `README`, `docs/`, changelogs | ASD-STE100 Simplified Technical English. |
| Commit messages and pull request descriptions | ASD-STE100 Simplified Technical English. |

Text in `.lmbrain-lite/` includes milestones, tasks, log lines, the note, knowledge pages, memory records, skills, design notes, dreams, and kit feedback.

These rules do not apply to:

- text that the operator writes;
- quoted text, for example error output or text from a third party;
- product text in a different language, when a specification gives it;
- existing text that you do not change.

When you change a section, make that section obey these rules. Do not rewrite the brain or the code in bulk without a task for it.

## What ASD-STE100 is

ASD-STE100 is a controlled form of English for technical text. It has writing rules and a dictionary of approved words. The specification is free. You can ask for the current issue at <https://www.asd-ste100.org>. This file gives a short summary of the rules. It is not a copy of the specification.

## The rules

### Words

1. Use approved words. Use each word only with its approved meaning. For example, "follow" means "come after". It does not mean "obey".
2. You can use technical names (for example `lite_digest`, `.mcp.json`, "worktree") and technical verbs of the project (for example "commit", "merge", "deploy").
3. Use one word for one thing. Do not use synonyms for variety.
4. Do not use phrasal verbs ("set up", "carry out", "find out"). Use one verb ("install", "do", "find").
5. Do not use the `-ing` form of a verb, except in a technical name.
6. Keep the articles ("a", "an", "the") and the short connecting words.
7. Do not make noun clusters of more than three words. Write "the timeout of the sync job", not "sync job timeout value".

### Sentences

8. A procedural sentence (an instruction) has a maximum of 20 words.
9. A descriptive sentence has a maximum of 25 words.
10. Write one instruction in each sentence. You can write two actions in one sentence only when you do them at the same time.
11. Write instructions in the imperative: "Run the tests."
12. Use the active voice: "The app writes the file", not "The file is written by the app".
13. Use only simple tenses: the present, the simple past, and the future.

### Paragraphs and lists

14. A descriptive paragraph has a maximum of six sentences.
15. Write each paragraph about one topic.
16. Use a vertical list for steps, conditions, and items.
17. Start a warning or a caution with a simple command: "Do not delete `.tokensave/`. The server uses it."

## Examples

| Do not write | Write |
| --- | --- |
| `- [ ] T-03 Setting up the code index so that it gets kept in sync by the app` | `- [ ] T-03 Add the code index. The app keeps the index current.` |
| `Ran tests, everything looks good apart from a couple of flaky ones that were already broken` | `Ran the tests. All tests pass, but two tests fail at random. They failed before this change.` |
| `// we need to bail out early here otherwise the lock gets held forever` | `// Stop here. If the function continues, it does not release the lock.` |
| `Ho aggiunto la validazione del form` | `Added the validation of the login form.` |

## Checks

The `lite_*` tools check the mechanical rules: sentence length, paragraph length, the passive voice, and text that is not in English. When a tool returns `writing_warnings`, the write is done. Correct the text with a second write. A `not-english` warning is a violation of this contract: always correct it.

The tools cannot check code, comments, or documentation in the repository. You must obey the rules there too.
