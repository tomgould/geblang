package com.dwgebler.geblang.run

import com.intellij.execution.lineMarker.ExecutorAction
import com.intellij.execution.lineMarker.RunLineMarkerContributor
import com.intellij.icons.AllIcons
import com.intellij.psi.PsiElement

/**
 * Gutter Run/Debug markers for Geblang: click-to-run `main()` and `@test`-decorated
 * test methods/classes.
 *
 * [getInfo] is invoked by the platform once per PSI leaf. Anchor detection itself
 * lives in [GeblangRunAnchors] (shared with [GeblangFileRunConfigurationProducer]),
 * which inspects a leaf together with its neighbouring leaves via
 * [PsiElement.getPrevSibling]/[PsiElement.getNextSibling] - the Geblang PSI tree is
 * FLAT (see [com.dwgebler.geblang.psi.GeblangParserDefinition]), so every leaf in a
 * `.gb` file is a direct child of the file with no nesting, and neighbouring leaves
 * are exactly the neighbouring tokens.
 *
 * Three anchors are recognised, each identified by exactly one leaf (so exactly one
 * gutter icon is produced per logical anchor - never one per token):
 *
 *  - **Run file**: the `main` IDENTIFIER leaf of a top-level `func main(` declaration.
 *  - **Run test class**: the class-name IDENTIFIER leaf of a `class <Name> extends
 *    test.Test` declaration.
 *  - **Run test method**: the method-name IDENTIFIER leaf of an `@test`-decorated
 *    `func <name>(` declaration.
 *
 * See [GeblangRunAnchors] for the exact leaf-shape each anchor requires.
 *
 * Each `Info` is built via the non-deprecated
 * `Info(Icon, AnAction[], Function<? super PsiElement, String>)` constructor, with
 * [ExecutorAction.getActions] `(0)` supplying the standard Run + Debug actions (the
 * `Info(Icon, Function, AnAction...)` overload is annotated `@Deprecated(forRemoval =
 * true)` in the IC-2024.2.4 platform jars). Those actions dispatch through the
 * platform's run-configuration-producer machinery, i.e. whichever
 * [com.intellij.execution.actions.RunConfigurationProducer] claims the context -
 * [GeblangFileRunConfigurationProducer] for the "Run file" anchor, and
 * [GeblangTestRunConfigurationProducer] for the two test anchors.
 */
class GeblangRunLineMarkerContributor : RunLineMarkerContributor() {

    override fun getInfo(element: PsiElement): Info? {
        if (GeblangRunAnchors.isTestMethodAnchor(element) || GeblangRunAnchors.isTestClassAnchor(element)) {
            return Info(
                AllIcons.RunConfigurations.TestState.Run,
                ExecutorAction.getActions(0),
                { e -> "Run '${e.text}'" }
            )
        }
        if (GeblangRunAnchors.isMainAnchor(element)) {
            return Info(
                AllIcons.Actions.Execute,
                ExecutorAction.getActions(0),
                { e -> "Run '${e.text}()'" }
            )
        }
        return null
    }
}
