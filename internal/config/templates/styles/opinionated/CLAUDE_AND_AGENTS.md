## Design & Programming Philosophies

Assume that we are using LLM Agents to do essentially all programming work now, not humans. Assumptions about cost/benefit and level of effort need to be radically rethought.

The Plan and the Test Harness are everything. These are more important than they've ever been. No amount of effort is too high to spend to get these absolutely possible. The code is malleable and can be written, re-written and refactored. It won't be able to be fully reviewed by humans, but as long as the test harnesses are solid, we can have confidence that it's correct.

We are aiming as much as possible to practice Massively Parallel Programming. Individual agents will not be monitored and their code will not be manually reviewed by humans. They will work on assigned tasks, and other agents will review the code and suggest improvements before merging. Like a parallel compute job running in a large data center, individual nodes may fail or drop out and the work will be picked up and re-routed to others. The important thing is to be proactive and keep the system moving to the end.

## Planning

DO NOT use built-in plan mode. Discuss all planning steps interactively with the user and other agents via "h2 send" messaging, and write planning docs in the repo according to project guidelines.

### Design Doc

For any moderately sized or bigger project, we'll want to write a design doc before implementing. DO NOT use the built-in plan mode, write plans in discussion with the user and write them into a markdown file that gets committed to the project.

The design doc should start with high level summarization of the approach, the architecture, and any unusual or unintuitive properties or decisions worth highlighting. It should include mermaid diagrams of Component Diagrams, Sequence Diagrams for key flows, State Diagrams when relevant, and any other kinds of diagrams that are helpful to communicate the structure without having to read the full detail of the doc.

From there, it should go into detail about things like package/module structure of the code, import flow assumptions of what should or should not import what, fully document any API/GRPC/other interfaces, then all major classes/structs/components and what properties and key methods they hold.

The design doc should lastly include a Testing section, with any details about unit testing (any unusual areas to test, mocking strategy for different components, etc.), component testing (what frameworks used, how to write tests, what to test vs what should be covered in unit tests or other methods), and integration testing (not necessarily the full e2e testing, but important areas where we will test multiple pieces/components in conjunction with each other).

### Design Review Docs

After design we should always do at least one, prefereably 2 rounds of design review. Remember the Plan and the Test Harness are everything. Reviewers should look for potential gaps or things that are wrong or overlooked and documentthem in a -review.md doc next to the plan. If there are multiple reviewers they should each write their own doc (without looking at the other -reviewer docs first).

Afterwards this feedback should be collapsed into the main review doc, and the main doc should be updated to state which review docs it incorporated feedback from.

### Testing Plan Docs

An entirely separate test plan doc should be written as well. Remember the Plan and the Test Harness are everything. This should not include the basic unit, component, and integration testing covered in the main design doc. This should be focused on additional test harnesses to build to ensure correctness. It should cover full blackbox e2e testing of all core flows. If we are porting or re-writing something, it should include a comparison testing plan of how to automate comparing the original implementation and newly written on to ensure correctness taht way. It should include load testing, if it would be helpful. As well as any other smoke testing or other testing patterns we can follow.

Lastly, it should also include manual qa testing plans that we should do. These should not just re-write deterministic tests, but if there are tests we can design that make use of human/agent judgement that we use as the manual testing step before every release to have additional certainty things are working, we should write these. If there are substantial external dependencies that we rely on, we should consider writing test versions of them that we can run our tests against.

All testing plans should be considered on the merits of "will they work well and provide additional assurance now and for future releases". If they don't work or don't provide any real incremental assurance, we don't need them. But we SHOULD NOT rule out any testing ideas because they seem like too much work or we're worried about ROI (remember, URP). If they will improve confidence, and they can be done even if it takes significant effort, they should be done.

### Shaping

We have a "Shaping" skill that can help with defining requirements and comparing alternative solutions. This is good to use while brainstorming and early on while figuring out what a solution looks like. It doesn't need to be used for every plan, but should be used before writing the official design doc if we're starting from a set of requirements without a prescribed solution. If requirements are vague, part of this exercise should including discussing with the user or other agents to more rigidly specify them.

## Techniques

These are techniques the user may prompt for that you should know about and be ready to integrate. You should also look for opporunties where it makes sense to introduce them on your own.

### Unreasonably Robust Programming (URP)

This is the idea that since agents are writing the code, we can do things that are valuable but where the ROI might seem unjustified due to perceived high cost. Do not worry about implementation effort and ROI of development. Do not worry about how many people are using this application or what the development budget is. The goal is to assume that we have an unlimited development budget and think about what we would build that would make our system more robust, stable, thoroughly tested, provably correct, less likely to cause outages, etc. Be creative and build something we can be proud of. Label any applications of Unreasonably Robust Programming in a section in the Design Doc.

Anything that can be measured should be measured, including test coverage, benchmarks, load tests, etc. We're not trying to over-engineer just for the sake of it. We need clear evidence, or if not possible then a reasonable hypothesis, that what we're building is actually helping in some tangible way. The way we will measure this should be clearly articulated in the plan (and of course measurement should be automated as part of the work).

### Alien Artifacts

This refers to employing advanced mathematics and computer science techniques to our features. State of the art research, ideas demonstrated in recent papers that are not well known, PhD level mathematics and research topics. This is a unique advantage we have. While most human programmers would not understand these techniques and how to apply them, agents can read papers, understand the complex ideas, and implement them easily. Label any clever Alien Artifacts that we're using in a section in the Design Doc.

### Extreme Optimization

Extreme optimization is a technique to ensure our code will run as fast and efficiently as possible. This should be extreme. For example, we should be writing code that runs in hot paths or tight loops in assembly utilizing SIMD for all major platforms (ARM64 NEON and X86_64 SSE & AVX2) as well as matrix math optimizations (offload to GPU, ARM SME, etc.) This may sometimes intersect with Alien Artifacts if there are exotic algorithms that can be used as part of this optimization, or with URP. Label all examples of Extreme Optimization in the Design Doc.

## Implementation

Depending on the size and scope of the task you may be working solo or in a team. Pay attention to instructions about working with a concierge, scheduler, coder, or reviewer and include them in appropriate work.

When writing code, prefer the Test Driven Development (TDD) approach: write unit tests that fail first, then write the missing code, and ensure tests pass. You should also include relevant integration level tests and extend end to end test suites where needed as new functionality is built, but writing entire external test harnesses, benchmarks, smoke tests, load tests, etc. can be separated from feature development work.

ALWAYS commit your changes after completing a chunk of implementation work and ensuring tests pass. Git commits can easily be ammended later if further tweaks are made, but committing ensures we won't accidentally delete or lose work.

## Language Specific Preferences

### Go

Go has a lot of standard idioms, so use them. There's not much that we want to override.

One technique that comes in handy is when implementing an interface, that will have several concrete providers that all implement it. Put the interface and shared types in a minimal package like internal/something/foo. Put the various implementations of it in nested packages like internal/something/foo/bar. bar the concrete implementation imports interface foo. Then, if needed, create a fooservice package that has e.g. a factory to select the right provider to use, and any shared business logic that applies to all providers.

## Workflow Tooling

### h2 Messaging Protocol

We use a tool called h2 which is an agent run-time, messaging protocol, and orchestration system.

Messages from other agents or users appear in your input prefixed with:
[h2 message from: <sender>]

When you receive an h2 message:

1. Acknowledge quickly: h2 send <sender> "Working on it..."
2. Do the work
3. Reply with results: h2 send <sender> "Here's what I found: ..."

Example:
[h2 message from: scheduler] Can you check the test coverage?

You should reply:
h2 send scheduler "Checking test coverage now"

# ... do the work ...

h2 send scheduler "Test coverage is 85%. Details: ..."

#### Available h2 Commands

- h2 list - See active agents and users
- h2 peek <name> - Check what another agent is doing
- h2 send <name> "msg" - Send message to agent or user
- h2 whoami - Check your agent name

If given a role within an h2 team or pod, stick to it and work collectively with the other agents to complete tasks.

### Beads task management

We use beads-lite, which is installed in the path as bd. It's a task
management tool that stores tasks in individual json files in a directory
like .beads/issues/<issue>.json.

Common commands are:

- bd create <name> --type epic --labels project=foo --description "<description>"
- bd create <name> --labels project=foo --description "<description>" --parent <epic-id>
- bd dep add B A --type blocks
- bd show A
- bd list
- bd ready

Note that the dep add B A command always creates an "B requires A to be done
first relationship", whether you specify the type as blocks or its inverse
depends_on (these are the default). There are other types like "tracks" and the
syntax for the dependency always goes in this same direction, unless the
relationship is bidirectional.

DO NOT use the beads executable as that is the original version of beads that
is slower, buggier, and not compatible with our tasks.

#### Rules for creating beads

1. Don't decompose tasks too small. Each task should be substantial enough
   that it takes a few hundred lines of code to implement, or if smaller it
   should have very clear, obvious boundaries. Splitting tasks too small can
   cause different agents to build similar or overlapping features with
   different coding patterns.

2. Include any unit test and integration test (if application) work in the
   same implementation task, don't split these up.

3. Create epics for large projects that contain multiple tasks.

4. Create dependencies between tasks. If there's uncertainty
   whether two tasks could maybe be done in parallel but might have some
   overlap, err on the side of creating the dependency between them. It's better
   to take a little bit longer doing work sequentially than parallelize too wide
   and end up with inconsistencies and duplication.

5. If there are a series of parallelizable tasks implementing many instances
   of the same pattern, create some dependencies between the later tasks and the
   first one or two. That way the rest of the tasks can follow the same pattern
   that has been established.

6. Create beads for follow-up work that arises. If you're working or
   reviewing another agent's work and notice an issue, create a bead to track it
   (usually in the same epic that the bead for the original work was in) in
   addition to messaging the agent on h2 about the issue.
