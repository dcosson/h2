---
name: prose-cleanup
description: Revise a document (plan, PR description, doc, AGENTS.md, skill, code comment block) so a reader who wasn't in the conversation can read it start to finish and understand it. Use when asked to clean up the prose, fix the writing or make a doc read normally, and before handing over a doc you wrote.
---

# prose-cleanup

Revise a draft for its intended reader. If the project has a writing or style
guide (for example `WRITING.md`, `STYLE.md`, or a section of `AGENTS.md` or
`CLAUDE.md`), read it first and follow it for the finished text. A public draft
(docs, guides, blog posts, announcements, UI copy) follows the public style
guide even while teammates review it. Preserve the voice and structure
appropriate to its form.

Read each sentence and rewrite it where it fails a check below. Don't rely on
searching the draft for particular words: most problems are in how a sentence
is built, and a word search misses them.

## Procedure

1. Write down, in one line, who will read the document, what they already know,
   and what they should learn or do. Identify the form: for example, a task
   guide, reference, blog post, announcement, or internal plan. For a skill or
   prompt, assume an agent with no conversation context.
2. Read the whole document once before you change anything. Some problems only
   show across sections: a summary that contradicts the body, or a section
   that still relies on a theory the rest of the doc dropped.
3. Go through the document sentence by sentence and apply the checklist.
4. Read the result once more, start to finish, as the reader from step 1.
5. If you write or change any text later in the session, run the checklist on
   that text before you hand it over.

## Checklist

For each sentence:

- **Subject.** Ask who or what does the action, and make that the subject: a
  person, "you", or a program, function, job, table or command that really
  does it. If the subject is a document, a page, a plan, a section or an idea,
  find who acts and rewrite the sentence around them.
- **Verb.** Make the action the main verb, and use the plainest verb that
  fits: add, change, remove, read, write, store, return, call, send, run,
  start, stop, fail, check, allow, refuse, say, list, show, give. If the verb
  isn't on this list, check whether one on the list says the same thing, and
  use it if so. A verb can look plain to you and still be one you overuse.
- **Names.** Check that every name is a code identifier, a term from the
  project's glossary (if it has one), or ordinary English the reader already
  uses. Replace
  anything else with a description of what it is. Spell out PR numbers,
  ticket IDs and labels from your own notes.
- **Mechanism.** Where the sentence describes behaviour, say what happens:
  which program does what, in which order, under which condition. Replace a
  dramatic summary of the effect with the condition itself.
- **Tense.** Use the present tense for how the system works now, the future
  for a change that is planned, and the past for what happened. A planned
  change written in the present tense reads as if it already exists.
- **Full sentence.** Keep the articles and verbs in paragraph text. Use
  complete sentences in durable prose. Fragments can fit lists, tables, UI
  labels, and informal chat when the applicable guide allows them.
- **Examples.** Check that every example (a quoted heading, a sample sentence,
  a sample list) makes sense to a reader who never saw the conversation it
  came from. Keep concrete examples from the project when they teach the point; explain
  missing context and remove private shorthand. Don't replace a useful example
  with a generic one merely because it came from the author's work.
- **Needed.** Ask whether the sentence changes what the reader knows or does.
  Delete it if not.

For each rule (in a skill, prompt, AGENTS.md or rule list), write it the way
you'd say it to the reader, such as "Open each question with some
background." Leave the explanations and observations around it as they are.

For the document as a whole:

- **Headings.** Use short, specific headings in sentence case. Use noun phrases
  for reference topics, verbs for tasks, and questions when the section answers
  them. Follow required templates and the collection's heading conventions.
- **Opening.** Give the title, the first sentence and the first paragraph the
  most attention, because many readers stop there. Check that they're written
  for the person who will read them and that they reach the real point
  without preamble: what changed, what the reader needs to decide, or what
  they need to know, and why it matters to them.
- **Audience.** For each paragraph, ask whether the reader wants it. Cut
  details that are there because they interested you or took effort to find,
  unless they help the reader understand or assess the point. Keep evidence and
  relevant history in blogs and investigations. A clearly written sentence the
  reader doesn't need still goes.
- **Current state.** Keep what is true and what will be built. Delete
  obsolete draft history and commentary on why the document exists. In guides
  and current system docs, fold resolved questions into the section they affect.
  In blog posts, investigations, and postmortems, keep the sequence of events
  when it explains the result or supports the author's reasoning.
- **Decisions.** Record a decision only if someone made it. If you're not sure
  a question was settled, write it as open. A decision nobody made is a
  correctness bug, because later work follows it.
- **Emphasis.** Keep at most one or two bold callouts or warnings in the
  document. Write invariants as plain statements. Keep emphasis that helps
  readers find a definition or important fact, and preserve meaningful UI
  formatting. Don't bold terms automatically on first use.
- **Ending.** End on the last useful content. Delete a closing recap; keep
  next steps that offer a new action, decision, or useful link.

For technical docs and examples, also check the teaching:

- Can the reader follow the recommended path with the stated setup and access?
- Does each example show an understandable result and make clear whether it is
  runnable on its own, continues earlier code, or is illustrative?
- Does the explanation help the reader predict behavior or choose an option?
  Are timing, defaults, permissions, and limits beside the operation they affect?
- Can the reader distinguish likely causes of a failure and confirm the fix?

Apply these checks where they fit the page's purpose; a reference entry does
not need a tutorial. Follow the project's style guide, if it has one, for the
writing principles and the collection's own instructions for validation. If
technical information is missing, flag the gap rather than inventing a
prerequisite, result, or guarantee.

## Editing a sentence

When you replace a word, use a plainer one. A fresher synonym makes the
sentence harder to read. After each change, run the subject, verb, tense and
names checks on the new sentence.

To shorten a draft, delete whole sentences and paragraphs. Keep the
articles and verbs in the sentences that stay.

If a draft has grown over many edits, rewriting it from the source is
often faster and better than editing it. Diff the rewrite against the old
version afterwards to check that no fact was lost.

## Examples

Sentences that pass the checklist:

- "We'll add a number input to the settings page. The settings handler will
  store the value with the rest of the user's settings."
- "The cleanup job deletes old build directories. It skips a directory while
  any running process still has it open."
- "A user can read only the files they have been granted. When the server
  lists a folder, it skips any file the user hasn't been granted."
- "Open each question to the user with two or three sentences of background,
  and explain any PR or ticket you mention."
- "Run `make lint` before you commit. The pre-commit hook runs only
  `ruff format`."

Headings that pass: "Storage", "Check the run status", "Permissions",
"Who can read this file?", "Out of scope", "Rollout".

## Leave alone

- Technical content: identifiers, file and line references, code, numbers.
- Bold that marks a real scope boundary or a destructive step.
- Parentheticals that give a reason.
- The author's voice, where it is already plain.

This is editing. Keep the technical content exactly. If a fix would change the
meaning, leave the sentence and point it out.
